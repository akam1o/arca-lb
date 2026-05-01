// Package reconciler provides per-VIP reconciliation for the agent.
// Each VIP gets its own goroutine that reacts to events from the K8s
// informer and from the health check engine. A background safety-net
// timer ensures drift is corrected even if events are missed.
package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	"github.com/akam1o/arca-lb/internal/agent/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("arca-lb/agent/reconciler")

const (
	// TuningDriftPolicyPreserve keeps retained dataplane state even when
	// tuning-only attributes differ from the desired configuration.
	TuningDriftPolicyPreserve = "preserve"

	// TuningDriftPolicyRollingRecreate withdraws the local route, waits for
	// traffic to drain, then recreates the VIP with the desired tuning.
	TuningDriftPolicyRollingRecreate = "rolling_recreate"
)

// TuningDriftConfig controls repair of forwarding-compatible retained VIPs
// that differ only in dataplane tuning.
type TuningDriftConfig struct {
	Policy        string
	DrainDuration time.Duration
}

// HealthTracker provides the current health state of backends.
type HealthTracker interface {
	// IsHealthy returns whether a backend is considered healthy.
	IsHealthy(vipKey, backendAddr string) bool
	// HealthyBackends returns the list of healthy backends for a VIP.
	HealthyBackends(vipKey string, backends []v1alpha1.BackendSpec) []v1alpha1.BackendSpec
}

// StatusUpdater updates VirtualIP status with the agent's observed backend health.
type StatusUpdater interface {
	UpdateVIPStatus(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error
}

// Manager manages per-VIP reconciler goroutines.
type Manager struct {
	dp            dataplane.DataPlane
	router        routing.Router
	store         *store.Store
	healthTracker HealthTracker
	statusUpdater StatusUpdater
	logger        *slog.Logger

	safetyInterval time.Duration
	tuningDrift    TuningDriftConfig

	mu     sync.RWMutex
	vips   map[string]*vipReconciler // key: namespace/name
	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager creates a new reconciler manager.
func NewManager(
	dp dataplane.DataPlane,
	router routing.Router,
	st *store.Store,
	ht HealthTracker,
	safetyInterval time.Duration,
	logger *slog.Logger,
) *Manager {
	if safetyInterval == 0 {
		safetyInterval = 30 * time.Second
	}
	return &Manager{
		dp:             dp,
		router:         router,
		store:          st,
		healthTracker:  ht,
		logger:         logger,
		safetyInterval: safetyInterval,
		tuningDrift:    normalizeTuningDriftConfig(TuningDriftConfig{}),
		vips:           make(map[string]*vipReconciler),
	}
}

// SetStatusUpdater wires the Kubernetes status updater used by new reconcilers.
func (m *Manager) SetStatusUpdater(updater StatusUpdater) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.statusUpdater = updater
	for _, vr := range m.vips {
		vr.statusUpdater = updater
	}
}

// SetTuningDriftConfig updates retained VIP tuning drift handling for new and
// existing reconcilers.
func (m *Manager) SetTuningDriftConfig(cfg TuningDriftConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tuningDrift = normalizeTuningDriftConfig(cfg)
	for _, vr := range m.vips {
		vr.tuningDrift = m.tuningDrift
	}
}

func normalizeTuningDriftConfig(cfg TuningDriftConfig) TuningDriftConfig {
	switch cfg.Policy {
	case TuningDriftPolicyPreserve, TuningDriftPolicyRollingRecreate:
	default:
		cfg.Policy = TuningDriftPolicyRollingRecreate
	}
	if cfg.DrainDuration == 0 {
		cfg.DrainDuration = 30 * time.Second
	}
	return cfg
}

// Start starts the manager.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.logger.Info("reconciler manager started")
}

// Stop stops all per-VIP reconcilers and the manager.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}

	reconcilers := make([]*vipReconciler, 0, len(m.vips))
	for _, vr := range m.vips {
		reconcilers = append(reconcilers, vr)
	}
	m.vips = make(map[string]*vipReconciler)
	m.mu.Unlock()

	for _, vr := range reconcilers {
		vr.stop()
	}

	m.logger.Info("reconciler manager stopped")
}

// OnVIPUpdate is called when a VirtualIP is created or updated.
func (m *Manager) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	key := vip.Namespace + "/" + vip.Name

	m.mu.Lock()

	vr, exists := m.vips[key]
	if !exists {
		vr = newVIPReconciler(key, m.dp, m.router, m.store, m.healthTracker, m.statusUpdater, m.safetyInterval, m.tuningDrift, m.logger, m.onVIPReconcilerStopped)
		m.vips[key] = vr
		go vr.run(m.ctx)
	}
	m.mu.Unlock()

	vr.update(vip.DeepCopy())
}

// OnVIPDelete is called when a VirtualIP is deleted.
func (m *Manager) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	key := vip.Namespace + "/" + vip.Name

	m.mu.Lock()
	vr, ok := m.vips[key]
	if ok {
		delete(m.vips, key)
	}
	m.mu.Unlock()

	if ok {
		// The goroutine will handle cleanup and exit. The manager removes the
		// entry immediately so a later add for the same namespace/name creates a
		// fresh reconciler instead of sending updates to the deleting goroutine.
		vr.markDeleted(vip.DeepCopy())
	}
}

func (m *Manager) onVIPReconcilerStopped(key string, stopped *vipReconciler) {
	var current *vipReconciler

	m.mu.Lock()
	if vr, ok := m.vips[key]; ok {
		if vr == stopped {
			delete(m.vips, key)
		} else {
			current = vr
		}
	}
	m.mu.Unlock()

	// If a VIP with the same namespace/name was recreated while the old
	// reconciler was still deleting dataplane state, reconcile the new object
	// after old cleanup completes.
	if current != nil {
		current.triggerReconcile()
	}
}

// OnHealthChange is called when a backend's health status changes.
func (m *Manager) OnHealthChange(vipKey string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if vr, ok := m.vips[vipKey]; ok {
		vr.triggerReconcile()
		m.logger.Debug("health change triggered reconcile", "key", vipKey)
	}
}

// --- per-VIP reconciler ---

type vipEvent struct {
	vip     *v1alpha1.VirtualIP
	deleted bool
}

type vipReconciler struct {
	key            string
	dp             dataplane.DataPlane
	router         routing.Router
	store          *store.Store
	healthTracker  HealthTracker
	statusUpdater  StatusUpdater
	safetyInterval time.Duration
	logger         *slog.Logger

	tuningDrift TuningDriftConfig

	eventCh     chan vipEvent
	reconcileCh chan struct{}
	stopCh      chan struct{}
	stopped     chan struct{}
	onStopped   func(key string, stopped *vipReconciler)

	mu      sync.RWMutex
	current *v1alpha1.VirtualIP
}

func newVIPReconciler(
	key string,
	dp dataplane.DataPlane,
	router routing.Router,
	st *store.Store,
	ht HealthTracker,
	statusUpdater StatusUpdater,
	safetyInterval time.Duration,
	tuningDrift TuningDriftConfig,
	logger *slog.Logger,
	onStopped func(key string, stopped *vipReconciler),
) *vipReconciler {
	return &vipReconciler{
		key:            key,
		dp:             dp,
		router:         router,
		store:          st,
		healthTracker:  ht,
		statusUpdater:  statusUpdater,
		safetyInterval: safetyInterval,
		tuningDrift:    normalizeTuningDriftConfig(tuningDrift),
		logger:         logger.With("vip", key),
		eventCh:        make(chan vipEvent, 8),
		reconcileCh:    make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		stopped:        make(chan struct{}),
		onStopped:      onStopped,
	}
}

func (vr *vipReconciler) update(vip *v1alpha1.VirtualIP) {
	select {
	case vr.eventCh <- vipEvent{vip: vip}:
	default:
		vr.logger.Warn("event channel full, dropping event")
	}
}

func (vr *vipReconciler) markDeleted(vip *v1alpha1.VirtualIP) {
	select {
	case vr.eventCh <- vipEvent{vip: vip, deleted: true}:
	default:
		vr.logger.Warn("event channel full for delete, forcing")
		// Force by draining and resending
		select {
		case <-vr.eventCh:
		default:
		}
		vr.eventCh <- vipEvent{vip: vip, deleted: true}
	}
}

func (vr *vipReconciler) triggerReconcile() {
	select {
	case vr.reconcileCh <- struct{}{}:
	default:
	}
}

func (vr *vipReconciler) stop() {
	select {
	case <-vr.stopCh:
	default:
		close(vr.stopCh)
	}
	<-vr.stopped
}

func (vr *vipReconciler) run(ctx context.Context) {
	defer func() {
		close(vr.stopped)
		if vr.onStopped != nil {
			vr.onStopped(vr.key, vr)
		}
	}()

	ticker := time.NewTicker(vr.safetyInterval)
	defer ticker.Stop()

	vr.logger.Info("per-VIP reconciler started")

	for {
		select {
		case <-ctx.Done():
			vr.logger.Info("per-VIP reconciler stopped (context)")
			return
		case <-vr.stopCh:
			vr.logger.Info("per-VIP reconciler stopped (explicit)")
			return

		case ev := <-vr.eventCh:
			if ev.deleted {
				vr.handleDelete(ctx, ev.vip)
				return // exit goroutine after delete
			}
			vr.mu.Lock()
			vr.current = ev.vip
			vr.mu.Unlock()
			vr.reconcile(ctx)

		case <-vr.reconcileCh:
			vr.reconcile(ctx)

		case <-ticker.C:
			vr.reconcile(ctx)
		}
	}
}

func (vr *vipReconciler) reconcile(ctx context.Context) {
	vr.mu.RLock()
	vip := vr.current
	vr.mu.RUnlock()

	if vip == nil {
		return
	}

	ctx, span := tracer.Start(ctx, "reconcile",
		trace.WithAttributes(
			attribute.String("vip.key", vr.key),
			attribute.String("vip.name", vip.Name),
			attribute.String("vip.address", vip.Spec.Address),
		),
	)
	defer span.End()

	vr.logger.Debug("reconciling VIP", "generation", vip.Generation)

	// Determine healthy backends
	var healthyBackends []v1alpha1.BackendSpec
	if vip.Spec.HealthCheck != nil && vr.healthTracker != nil {
		healthyBackends = vr.healthTracker.HealthyBackends(vr.key, vip.Spec.Backends)
	} else {
		// No health check → all backends are healthy
		healthyBackends = vip.Spec.Backends
	}
	hasHealthy := len(healthyBackends) > 0

	// Apply to data plane
	if err := vr.dp.ApplyVIP(ctx, vip, healthyBackends); err != nil {
		vr.logger.Error("failed to apply VIP to data plane", "error", err)
		span.RecordError(err)
		return
	}

	if drifts := dataplaneTuningDrifts(vr.dp, vr.key); len(drifts) > 0 {
		if err := vr.repairTuningDrift(ctx, vip, healthyBackends, hasHealthy, drifts); err != nil {
			vr.logger.Error("failed to repair retained VIP tuning drift", "error", err, "drifts", drifts)
			span.RecordError(err)
			return
		}
	}

	if vr.statusUpdater != nil {
		if err := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends); err != nil {
			vr.logger.Warn("failed to update VirtualIP status", "error", err)
			span.RecordError(err)
		}
	}

	// Manage BGP route
	if hasHealthy {
		if err := vr.router.AnnounceVIP(ctx, vip.Spec.Address); err != nil {
			vr.logger.Error("failed to announce route", "error", err)
			span.RecordError(err)
		}
	} else {
		if err := vr.router.WithdrawVIP(ctx, vip.Spec.Address); err != nil {
			vr.logger.Error("failed to withdraw route", "error", err)
			span.RecordError(err)
		}
	}

	// Persist last-applied config
	if vr.store != nil {
		data, err := json.Marshal(vip.Spec)
		if err == nil {
			if err := vr.store.SaveLastConfig(vr.key, data); err != nil {
				vr.logger.Warn("failed to persist last config", "error", err)
			}
		}
	}

	vr.logger.Info("VIP reconciled",
		"healthy", len(healthyBackends),
		"total", len(vip.Spec.Backends),
		"route_announced", hasHealthy)
}

func dataplaneTuningDrifts(dp dataplane.DataPlane, vipKey string) []dataplane.VIPTuningDrift {
	reporter, ok := dp.(dataplane.TuningDriftReporter)
	if !ok {
		return nil
	}
	return reporter.TuningDrifts(vipKey)
}

func (vr *vipReconciler) repairTuningDrift(
	ctx context.Context,
	vip *v1alpha1.VirtualIP,
	healthyBackends []v1alpha1.BackendSpec,
	hasHealthy bool,
	drifts []dataplane.VIPTuningDrift,
) error {
	cfg := normalizeTuningDriftConfig(vr.tuningDrift)
	if cfg.Policy == TuningDriftPolicyPreserve {
		vr.logger.Warn("retained VIP tuning drift preserved", "drifts", drifts)
		return nil
	}

	recreator, ok := vr.dp.(dataplane.VIPRecreator)
	if !ok {
		return fmt.Errorf("dataplane does not support VIP recreation")
	}

	vr.logger.Info("repairing retained VIP tuning drift", "policy", cfg.Policy, "drain", cfg.DrainDuration, "drifts", drifts)
	if err := vr.router.WithdrawVIP(ctx, vip.Spec.Address); err != nil {
		return fmt.Errorf("failed to withdraw route before VIP recreate: %w", err)
	}
	if hasHealthy {
		if err := sleepContext(ctx, cfg.DrainDuration); err != nil {
			return err
		}
	}
	if err := recreator.RecreateVIP(ctx, vip, healthyBackends); err != nil {
		return fmt.Errorf("failed to recreate VIP: %w", err)
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (vr *vipReconciler) handleDelete(ctx context.Context, vip *v1alpha1.VirtualIP) {
	ctx, span := tracer.Start(ctx, "delete",
		trace.WithAttributes(
			attribute.String("vip.key", vr.key),
			attribute.String("vip.name", vip.Name),
		),
	)
	defer span.End()

	vr.logger.Info("handling VIP deletion")

	// Remove from data plane
	if err := vr.dp.RemoveVIP(ctx, vip); err != nil {
		vr.logger.Error("failed to remove VIP from data plane", "error", err)
		span.RecordError(err)
	}

	// Withdraw BGP route
	if err := vr.router.WithdrawVIP(ctx, vip.Spec.Address); err != nil {
		vr.logger.Error("failed to withdraw route on delete", "error", err)
		span.RecordError(err)
	}

	// Clean up local state
	if vr.store != nil {
		if err := vr.store.DeleteLastConfig(vr.key); err != nil {
			vr.logger.Warn("failed to delete last config", "error", err)
		}
		if err := vr.store.DeleteHealthStatesForVIP(vr.key); err != nil {
			vr.logger.Warn("failed to delete health states", "error", err)
		}
	}

	vr.logger.Info("VIP deletion handled")
}

// GetStatus returns the list of VIP keys being managed.
func (m *Manager) GetStatus() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.vips))
	for k := range m.vips {
		keys = append(keys, k)
	}
	return keys
}

// ReconcileAll triggers reconciliation for all managed VIPs.
func (m *Manager) ReconcileAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, vr := range m.vips {
		vr.triggerReconcile()
	}
}

// Reconcile returns an error description for user-facing diagnostics.
func (m *Manager) Reconcile(key string) error {
	m.mu.RLock()
	vr, ok := m.vips[key]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("VIP %s not found", key)
	}

	vr.triggerReconcile()
	return nil
}
