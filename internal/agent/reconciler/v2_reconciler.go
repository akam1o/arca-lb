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
	"sort"
	"sync"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/agent/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var tracer = otel.Tracer("arca-lb/agent/reconciler")

const (
	// TuningDriftPolicyPreserve keeps retained dataplane state even when
	// tuning-only attributes differ from the desired configuration.
	TuningDriftPolicyPreserve = "preserve"

	// TuningDriftPolicyRollingRecreate drains the VIP address route, waits for
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
	UpdateVIPStatus(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec, conditions ...metav1.Condition) error
}

// RolloutCoordinator serializes disruptive VIP changes across agents.
type RolloutCoordinator interface {
	RunExclusive(ctx context.Context, key string, fn func(context.Context) error) error
}

// Manager manages per-VIP reconciler goroutines.
type Manager struct {
	dp            dataplane.DataPlane
	store         *store.Store
	healthTracker HealthTracker
	statusUpdater StatusUpdater
	rollouts      RolloutCoordinator
	routes        *routeCoordinator
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
		store:          st,
		healthTracker:  ht,
		routes:         newRouteCoordinator(router, logger),
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

// SetRolloutCoordinator wires cluster-wide serialization for disruptive VIP changes.
func (m *Manager) SetRolloutCoordinator(coordinator RolloutCoordinator) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rollouts = coordinator
	for _, vr := range m.vips {
		vr.rollouts = coordinator
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

type routeCoordinator struct {
	router routing.Router
	logger *slog.Logger

	mu           sync.Mutex
	addresses    map[string]*routeAddressState // key: VIP address
	vipAddresses map[string]string             // key: namespace/name
}

type routeAddressState struct {
	serving    map[string]bool // key: namespace/name
	advertised bool
	reconciled bool
	drainOwner string
}

func newRouteCoordinator(router routing.Router, logger *slog.Logger) *routeCoordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &routeCoordinator{
		router:       router,
		logger:       logger.With("component", "route-coordinator"),
		addresses:    make(map[string]*routeAddressState),
		vipAddresses: make(map[string]string),
	}
}

func (c *routeCoordinator) SetServing(ctx context.Context, vipKey, address string, serving bool) (bool, error) {
	if c == nil || c.router == nil || address == "" {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if previousAddress := c.vipAddresses[vipKey]; previousAddress != "" && previousAddress != address {
		if err := c.removeVIPLocked(ctx, vipKey, previousAddress); err != nil {
			return false, fmt.Errorf("failed to withdraw previous VIP address %s for %s: %w", previousAddress, vipKey, err)
		}
	}

	c.vipAddresses[vipKey] = address
	state := c.addresses[address]
	if state == nil {
		state = &routeAddressState{
			serving: make(map[string]bool),
		}
		c.addresses[address] = state
	}
	state.serving[vipKey] = serving

	return c.reconcileLocked(ctx, address, state)
}

// BeginDrain withdraws an address route only when no sibling VIP on that
// address is serving. While held, later SetServing calls cannot re-announce it.
func (c *routeCoordinator) BeginDrain(ctx context.Context, vipKey, address string) (bool, error) {
	if c == nil || c.router == nil || address == "" {
		return true, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if previousAddress := c.vipAddresses[vipKey]; previousAddress != "" && previousAddress != address {
		if err := c.removeVIPLocked(ctx, vipKey, previousAddress); err != nil {
			return false, fmt.Errorf("failed to withdraw previous VIP address %s for %s: %w", previousAddress, vipKey, err)
		}
		delete(c.vipAddresses, vipKey)
	}

	state := c.addresses[address]
	if state == nil {
		state = &routeAddressState{
			serving: make(map[string]bool),
		}
		c.addresses[address] = state
	}

	if state.drainOwner != "" && state.drainOwner != vipKey {
		return false, nil
	}
	for peerKey, serving := range state.serving {
		if peerKey != vipKey && serving {
			return false, nil
		}
	}

	c.vipAddresses[vipKey] = address
	state.drainOwner = vipKey
	state.serving[vipKey] = false
	advertised, err := c.reconcileLocked(ctx, address, state)
	if err != nil {
		state.drainOwner = ""
		return false, err
	}
	return !advertised, nil
}

// FinishDrain releases a drain held by BeginDrain and reconciles the route.
func (c *routeCoordinator) FinishDrain(ctx context.Context, vipKey, address string) (bool, error) {
	if c == nil || c.router == nil || address == "" {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.addresses[address]
	if state == nil {
		return false, nil
	}
	if state.drainOwner != vipKey {
		return state.advertised, nil
	}

	state.drainOwner = ""
	return c.reconcileLocked(ctx, address, state)
}

func (c *routeCoordinator) pendingAddressChange(vipKey, address string) (string, bool) {
	if c == nil || c.router == nil || address == "" {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	previousAddress := c.vipAddresses[vipKey]
	return previousAddress, previousAddress != "" && previousAddress != address
}

func (c *routeCoordinator) prepareAddressChange(ctx context.Context, vipKey, address string) (bool, error) {
	if c == nil || c.router == nil || address == "" {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	previousAddress := c.vipAddresses[vipKey]
	if previousAddress == "" || previousAddress == address {
		if state := c.addresses[address]; state != nil {
			return state.advertised, nil
		}
		return false, nil
	}

	if err := c.removeVIPLocked(ctx, vipKey, previousAddress); err != nil {
		advertised := false
		if state := c.addresses[previousAddress]; state != nil {
			advertised = state.advertised
		}
		return advertised, fmt.Errorf("failed to withdraw previous VIP address %s for %s: %w", previousAddress, vipKey, err)
	}
	delete(c.vipAddresses, vipKey)
	return false, nil
}

func (c *routeCoordinator) Delete(ctx context.Context, vipKey, address string) (bool, error) {
	if c == nil || c.router == nil {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if trackedAddress := c.vipAddresses[vipKey]; trackedAddress != "" {
		address = trackedAddress
	}
	delete(c.vipAddresses, vipKey)
	if address == "" {
		return false, nil
	}

	return false, c.removeVIPLocked(ctx, vipKey, address)
}

func (c *routeCoordinator) removeVIPLocked(ctx context.Context, vipKey, address string) error {
	state := c.addresses[address]
	if state == nil {
		return nil
	}

	delete(state.serving, vipKey)
	_, err := c.reconcileLocked(ctx, address, state)
	if len(state.serving) == 0 && !state.advertised {
		delete(c.addresses, address)
	}
	return err
}

func (c *routeCoordinator) reconcileLocked(ctx context.Context, address string, state *routeAddressState) (bool, error) {
	shouldAdvertise := false
	if state.drainOwner == "" {
		for _, serving := range state.serving {
			if serving {
				shouldAdvertise = true
				break
			}
		}
	}

	if shouldAdvertise {
		if state.advertised {
			return true, nil
		}
		if err := c.router.AnnounceVIP(ctx, address); err != nil {
			return state.advertised, err
		}
		state.advertised = true
		state.reconciled = true
		return true, nil
	}

	if state.advertised || !state.reconciled {
		if err := c.router.WithdrawVIP(ctx, address); err != nil {
			return state.advertised, err
		}
		state.advertised = false
		state.reconciled = true
	}
	return false, nil
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
		vr = newVIPReconciler(key, m.dp, m.routes, m.store, m.healthTracker, m.statusUpdater, m.rollouts, m.safetyInterval, m.tuningDrift, m.logger, m.onVIPReconcilerStopped)
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
	routes         *routeCoordinator
	store          *store.Store
	healthTracker  HealthTracker
	statusUpdater  StatusUpdater
	rollouts       RolloutCoordinator
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
	routes *routeCoordinator,
	st *store.Store,
	ht HealthTracker,
	statusUpdater StatusUpdater,
	rollouts RolloutCoordinator,
	safetyInterval time.Duration,
	tuningDrift TuningDriftConfig,
	logger *slog.Logger,
	onStopped func(key string, stopped *vipReconciler),
) *vipReconciler {
	return &vipReconciler{
		key:            key,
		dp:             dp,
		routes:         routes,
		store:          st,
		healthTracker:  ht,
		statusUpdater:  statusUpdater,
		rollouts:       rollouts,
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
	vr.mu.Lock()
	vr.current = vip
	vr.mu.Unlock()
	vr.triggerReconcile()
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

	if vr.rollouts != nil {
		previousAddress, pending := vr.routes.pendingAddressChange(vr.key, vip.Spec.Address)
		if pending {
			keys := rolloutKeysForAddresses(previousAddress, vip.Spec.Address)
			if err := runExclusiveRollouts(ctx, vr.rollouts, keys, func(ctx context.Context) error {
				if !vr.isCurrent(vip) {
					vr.logger.Debug("skipping stale VIP address rollout", "generation", vip.Generation)
					return nil
				}
				vr.reconcileApplied(ctx, span, vip, healthyBackends, hasHealthy, true)
				return nil
			}); err != nil {
				vr.logger.Error("failed to coordinate VIP address rollout", "error", err, "rollouts", keys)
				span.RecordError(err)
			}
			return
		}
	}

	vr.reconcileApplied(ctx, span, vip, healthyBackends, hasHealthy, false)
}

func runExclusiveRollouts(ctx context.Context, rollouts RolloutCoordinator, keys []string, fn func(context.Context) error) error {
	if rollouts == nil || len(keys) == 0 {
		return fn(ctx)
	}

	var run func(context.Context, int) error
	run = func(ctx context.Context, index int) error {
		if index >= len(keys) {
			return fn(ctx)
		}
		key := keys[index]
		return rollouts.RunExclusive(ctx, key, func(ctx context.Context) error {
			return run(ctx, index+1)
		})
	}
	return run(ctx, 0)
}

func rolloutKeysForAddresses(addresses ...string) []string {
	seen := make(map[string]struct{}, len(addresses))
	keys := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address == "" {
			continue
		}
		key := rolloutKeyForAddress(address)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func rolloutKeyForAddress(address string) string {
	return "vip-address/" + address
}

func (vr *vipReconciler) reconcileApplied(
	ctx context.Context,
	span trace.Span,
	vip *v1alpha1.VirtualIP,
	healthyBackends []v1alpha1.BackendSpec,
	hasHealthy bool,
	rolloutHeld bool,
) {
	routeAdvertised, routeErr := vr.routes.prepareAddressChange(ctx, vr.key, vip.Spec.Address)
	if routeErr != nil {
		vr.logger.Error("failed to prepare VIP address route change", "error", routeErr)
		span.RecordError(routeErr)
		if vr.statusUpdater != nil {
			if err := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
				servingCondition(vip, len(healthyBackends)),
				routeAdvertisedCondition(vip, routeAdvertised, routeErr),
			); err != nil {
				vr.logger.Warn("failed to update VirtualIP status", "error", err)
				span.RecordError(err)
			}
		}
		return
	}

	// Apply to data plane
	if err := vr.dp.ApplyVIP(ctx, vip, healthyBackends); err != nil {
		vr.logger.Error("failed to apply VIP to data plane", "error", err)
		span.RecordError(err)
		routeAdvertised, routeErr = vr.routes.SetServing(ctx, vr.key, vip.Spec.Address, false)
		if routeErr != nil {
			vr.logger.Error("failed to withdraw VIP address route after data plane apply failure", "error", routeErr)
			span.RecordError(routeErr)
		}
		if vr.statusUpdater != nil {
			if statusErr := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
				dataplaneApplyFailedCondition(err),
				routeAdvertisedCondition(vip, routeAdvertised, routeErr),
			); statusErr != nil {
				vr.logger.Warn("failed to update VirtualIP status", "error", statusErr)
				span.RecordError(statusErr)
			}
		}
		return
	}

	if drifts := dataplaneTuningDrifts(vr.dp, vr.key); len(drifts) > 0 {
		if err := vr.repairTuningDrift(ctx, vip, healthyBackends, hasHealthy, drifts, rolloutHeld); err != nil {
			vr.logger.Error("failed to repair retained VIP tuning drift", "error", err, "drifts", drifts)
			span.RecordError(err)
			return
		}
	}

	routeAdvertised, routeErr = vr.routes.SetServing(ctx, vr.key, vip.Spec.Address, hasHealthy)
	if routeErr != nil {
		vr.logger.Error("failed to reconcile VIP address route", "error", routeErr)
		span.RecordError(routeErr)
	}

	if vr.statusUpdater != nil {
		if err := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
			servingCondition(vip, len(healthyBackends)),
			routeAdvertisedCondition(vip, routeAdvertised, routeErr),
		); err != nil {
			vr.logger.Warn("failed to update VirtualIP status", "error", err)
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
		"serving", hasHealthy,
		"route_advertised", routeAdvertised)
}

func (vr *vipReconciler) isCurrent(vip *v1alpha1.VirtualIP) bool {
	vr.mu.RLock()
	defer vr.mu.RUnlock()

	return vr.current != nil &&
		vip != nil &&
		vr.current.UID == vip.UID &&
		vr.current.Generation == vip.Generation
}

func servingCondition(vip *v1alpha1.VirtualIP, healthyBackends int) metav1.Condition {
	condition := metav1.Condition{
		Type: agentstatus.ConditionServing,
	}
	if healthyBackends > 0 {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "BackendsHealthy"
		condition.Message = fmt.Sprintf("%d healthy backend(s) available for %s:%d/%s",
			healthyBackends, vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
		return condition
	}

	condition.Status = metav1.ConditionFalse
	if len(vip.Spec.Backends) == 0 {
		condition.Reason = "NoBackends"
		condition.Message = fmt.Sprintf("No backends configured for %s:%d/%s",
			vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
		return condition
	}

	condition.Reason = "NoHealthyBackends"
	condition.Message = fmt.Sprintf("No healthy backends available for %s:%d/%s",
		vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
	return condition
}

func dataplaneApplyFailedCondition(applyErr error) metav1.Condition {
	message := "Failed to apply VIP to data plane"
	if applyErr != nil {
		message = applyErr.Error()
	}
	return metav1.Condition{
		Type:    agentstatus.ConditionServing,
		Status:  metav1.ConditionUnknown,
		Reason:  "DataPlaneApplyFailed",
		Message: message,
	}
}

func routeAdvertisedCondition(vip *v1alpha1.VirtualIP, advertised bool, routeErr error) metav1.Condition {
	condition := metav1.Condition{
		Type: agentstatus.ConditionRouteAdvertised,
	}
	if routeErr != nil {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "RouteUpdateFailed"
		condition.Message = routeErr.Error()
		return condition
	}
	if advertised {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Advertised"
		condition.Message = fmt.Sprintf("VIP address %s is advertised by this node", vip.Spec.Address)
		return condition
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = "NotAdvertised"
	condition.Message = fmt.Sprintf("VIP address %s is not advertised by this node", vip.Spec.Address)
	return condition
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
	rolloutHeld bool,
) error {
	cfg := normalizeTuningDriftConfig(vr.tuningDrift)
	if cfg.Policy == TuningDriftPolicyPreserve {
		vr.logger.Warn("retained VIP tuning drift preserved", "drifts", drifts)
		return nil
	}

	if vr.rollouts != nil && !rolloutHeld {
		return runExclusiveRollouts(ctx, vr.rollouts, rolloutKeysForAddresses(vip.Spec.Address), func(ctx context.Context) error {
			if !vr.isCurrent(vip) {
				vr.logger.Debug("skipping stale retained VIP tuning drift repair", "generation", vip.Generation)
				return nil
			}
			return vr.repairTuningDrift(ctx, vip, healthyBackends, hasHealthy, drifts, true)
		})
	}

	recreator, ok := vr.dp.(dataplane.VIPRecreator)
	if !ok {
		return fmt.Errorf("dataplane does not support VIP recreation")
	}

	vr.logger.Info("repairing retained VIP tuning drift", "policy", cfg.Policy, "drain", cfg.DrainDuration, "drifts", drifts)
	drained, err := vr.routes.BeginDrain(ctx, vr.key, vip.Spec.Address)
	if err != nil {
		return fmt.Errorf("failed to drain route before VIP recreate: %w", err)
	}
	if !drained {
		vr.logger.Warn("skipping retained VIP tuning drift repair until VIP address can drain", "address", vip.Spec.Address, "drifts", drifts)
		return nil
	}
	releaseDrain := func(retErr error) error {
		if _, releaseErr := vr.routes.FinishDrain(ctx, vr.key, vip.Spec.Address); releaseErr != nil {
			if retErr != nil {
				return fmt.Errorf("%w; additionally failed to release route drain: %v", retErr, releaseErr)
			}
			return fmt.Errorf("failed to release route drain after VIP recreate: %w", releaseErr)
		}
		return retErr
	}
	if hasHealthy {
		if err := sleepContext(ctx, cfg.DrainDuration); err != nil {
			return releaseDrain(err)
		}
	}
	if err := recreator.RecreateVIP(ctx, vip, healthyBackends); err != nil {
		return releaseDrain(fmt.Errorf("failed to recreate VIP: %w", err))
	}
	return releaseDrain(nil)
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

	// Reconcile the shared VIP address route after this listener is removed.
	if _, err := vr.routes.Delete(ctx, vr.key, vip.Spec.Address); err != nil {
		vr.logger.Error("failed to reconcile route on delete", "error", err)
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
