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

// HealthTracker provides the current health state of backends.
type HealthTracker interface {
	// IsHealthy returns whether a backend is considered healthy.
	IsHealthy(vipName, backendAddr string) bool
	// HealthyBackends returns the list of healthy backends for a VIP.
	HealthyBackends(vipName string, backends []v1alpha1.BackendSpec) []v1alpha1.BackendSpec
}

// Manager manages per-VIP reconciler goroutines.
type Manager struct {
	dp            dataplane.DataPlane
	router        routing.Router
	store         *store.Store
	healthTracker HealthTracker
	logger        *slog.Logger

	safetyInterval time.Duration

	mu    sync.RWMutex
	vips  map[string]*vipReconciler // key: namespace/name
	ctx   context.Context
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
		vips:           make(map[string]*vipReconciler),
	}
}

// Start starts the manager.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.logger.Info("reconciler manager started")
}

// Stop stops all per-VIP reconcilers and the manager.
func (m *Manager) Stop() {
	m.cancel()

	m.mu.Lock()
	for _, vr := range m.vips {
		vr.stop()
	}
	m.vips = make(map[string]*vipReconciler)
	m.mu.Unlock()

	m.logger.Info("reconciler manager stopped")
}

// OnVIPUpdate is called when a VirtualIP is created or updated.
func (m *Manager) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := vip.Namespace + "/" + vip.Name

	vr, exists := m.vips[key]
	if !exists {
		vr = newVIPReconciler(key, m.dp, m.router, m.store, m.healthTracker, m.safetyInterval, m.logger)
		m.vips[key] = vr
		go vr.run(m.ctx)
	}

	vr.update(vip.DeepCopy())
}

// OnVIPDelete is called when a VirtualIP is deleted.
func (m *Manager) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := vip.Namespace + "/" + vip.Name
	if vr, ok := m.vips[key]; ok {
		vr.markDeleted(vip.DeepCopy())
		// The goroutine will handle cleanup and exit
	}
}

// OnHealthChange is called when a backend's health status changes.
func (m *Manager) OnHealthChange(vipName string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Find the reconciler by VIP name (try all namespaces)
	for key, vr := range m.vips {
		if vr.vipName() == vipName {
			vr.triggerReconcile()
			m.logger.Debug("health change triggered reconcile", "key", key)
			return
		}
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
	safetyInterval time.Duration
	logger         *slog.Logger

	eventCh    chan vipEvent
	reconcileCh chan struct{}
	stopCh     chan struct{}
	stopped    chan struct{}

	mu      sync.RWMutex
	current *v1alpha1.VirtualIP
}

func newVIPReconciler(
	key string,
	dp dataplane.DataPlane,
	router routing.Router,
	st *store.Store,
	ht HealthTracker,
	safetyInterval time.Duration,
	logger *slog.Logger,
) *vipReconciler {
	return &vipReconciler{
		key:            key,
		dp:             dp,
		router:         router,
		store:          st,
		healthTracker:  ht,
		safetyInterval: safetyInterval,
		logger:         logger.With("vip", key),
		eventCh:        make(chan vipEvent, 8),
		reconcileCh:    make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		stopped:        make(chan struct{}),
	}
}

func (vr *vipReconciler) vipName() string {
	vr.mu.RLock()
	defer vr.mu.RUnlock()
	if vr.current != nil {
		return vr.current.Name
	}
	return ""
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
	defer close(vr.stopped)

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
			attribute.String("vip.name", vip.Name),
			attribute.String("vip.address", vip.Spec.Address),
		),
	)
	defer span.End()

	vr.logger.Debug("reconciling VIP", "generation", vip.Generation)

	// Determine healthy backends
	var healthyBackends []v1alpha1.BackendSpec
	if vip.Spec.HealthCheck != nil && vr.healthTracker != nil {
		healthyBackends = vr.healthTracker.HealthyBackends(vip.Name, vip.Spec.Backends)
	} else {
		// No health check → all backends are healthy
		healthyBackends = vip.Spec.Backends
	}

	// Apply to data plane
	if err := vr.dp.ApplyVIP(ctx, vip, healthyBackends); err != nil {
		vr.logger.Error("failed to apply VIP to data plane", "error", err)
		span.RecordError(err)
		return
	}

	// Manage BGP route
	hasHealthy := len(healthyBackends) > 0
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
			if err := vr.store.SaveLastConfig(vip.Name, data); err != nil {
				vr.logger.Warn("failed to persist last config", "error", err)
			}
		}
	}

	vr.logger.Info("VIP reconciled",
		"healthy", len(healthyBackends),
		"total", len(vip.Spec.Backends),
		"route_announced", hasHealthy)
}

func (vr *vipReconciler) handleDelete(ctx context.Context, vip *v1alpha1.VirtualIP) {
	ctx, span := tracer.Start(ctx, "delete",
		trace.WithAttributes(attribute.String("vip.name", vip.Name)),
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
		if err := vr.store.DeleteLastConfig(vip.Name); err != nil {
			vr.logger.Warn("failed to delete last config", "error", err)
		}
		if err := vr.store.DeleteHealthStatesForVIP(vip.Name); err != nil {
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
