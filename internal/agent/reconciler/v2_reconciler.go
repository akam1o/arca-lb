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

	defaultDeleteRetryInterval = time.Second
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

	mu               sync.RWMutex
	vips             map[string]*vipReconciler // key: namespace/name
	deleting         map[string]*vipReconciler // key: namespace/name
	pendingRecreates map[string]*v1alpha1.VirtualIP
	ctx              context.Context
	cancel           context.CancelFunc
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
		dp:               dp,
		store:            st,
		healthTracker:    ht,
		routes:           newRouteCoordinator(router, logger),
		logger:           logger,
		safetyInterval:   safetyInterval,
		tuningDrift:      normalizeTuningDriftConfig(TuningDriftConfig{}),
		vips:             make(map[string]*vipReconciler),
		deleting:         make(map[string]*vipReconciler),
		pendingRecreates: make(map[string]*v1alpha1.VirtualIP),
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
	return c.beginDrain(ctx, vipKey, address, false)
}

// BeginAddressDrain withdraws an address route even when sibling VIPs on the
// same address are serving. It is used for local dataplane changes that can
// disrupt every listener on the address.
func (c *routeCoordinator) BeginAddressDrain(ctx context.Context, vipKey, address string) (bool, error) {
	return c.beginDrain(ctx, vipKey, address, true)
}

func (c *routeCoordinator) beginDrain(ctx context.Context, vipKey, address string, forceAddress bool) (bool, error) {
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
	if !forceAddress {
		for peerKey, serving := range state.serving {
			if peerKey != vipKey && serving {
				return false, nil
			}
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

	reconcilers := make([]*vipReconciler, 0, len(m.vips)+len(m.deleting))
	seen := make(map[*vipReconciler]struct{}, len(m.vips)+len(m.deleting))
	for _, vr := range m.vips {
		if _, ok := seen[vr]; !ok {
			seen[vr] = struct{}{}
			reconcilers = append(reconcilers, vr)
		}
	}
	for _, vr := range m.deleting {
		if _, ok := seen[vr]; !ok {
			seen[vr] = struct{}{}
			reconcilers = append(reconcilers, vr)
		}
	}
	m.vips = make(map[string]*vipReconciler)
	m.deleting = make(map[string]*vipReconciler)
	m.pendingRecreates = make(map[string]*v1alpha1.VirtualIP)
	m.mu.Unlock()

	for _, vr := range reconcilers {
		vr.stop()
	}

	m.logger.Info("reconciler manager stopped")
}

// OnVIPUpdate is called when a VirtualIP is created or updated.
func (m *Manager) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	key := vip.Namespace + "/" + vip.Name
	desired := vip.DeepCopy()

	m.mu.Lock()
	if _, deleting := m.deleting[key]; deleting {
		m.pendingRecreates[key] = desired
		m.mu.Unlock()
		return
	}

	vr, exists := m.vips[key]
	if !exists {
		vr = newVIPReconciler(key, m.dp, m.routes, m.store, m.healthTracker, m.statusUpdater, m.rollouts, m.safetyInterval, m.tuningDrift, m.logger, m.onVIPReconcilerStopped)
		m.vips[key] = vr
		go vr.run(m.ctx)
	}
	m.mu.Unlock()

	vr.update(desired)
}

// OnVIPDelete is called when a VirtualIP is deleted.
func (m *Manager) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	key := vip.Namespace + "/" + vip.Name

	m.mu.Lock()
	vr, ok := m.vips[key]
	if ok {
		delete(m.vips, key)
		m.deleting[key] = vr
	} else if pending, pendingOK := m.pendingRecreates[key]; pendingOK && pending.UID == vip.UID {
		delete(m.pendingRecreates, key)
	}
	m.mu.Unlock()

	if ok {
		// The goroutine will handle cleanup and exit. Recreates for this key are
		// queued until cleanup succeeds so a stale delete retry cannot remove a
		// freshly applied VIP with the same namespace/name.
		vr.markDeleted(vip.DeepCopy())
	}
}

func (m *Manager) onVIPReconcilerStopped(key string, stopped *vipReconciler) {
	var current *vipReconciler
	var recreated *vipReconciler
	var pendingVIP *v1alpha1.VirtualIP

	m.mu.Lock()
	if vr, ok := m.deleting[key]; ok && vr == stopped {
		delete(m.deleting, key)
		if pending := m.pendingRecreates[key]; pending != nil {
			delete(m.pendingRecreates, key)
			recreated = newVIPReconciler(key, m.dp, m.routes, m.store, m.healthTracker, m.statusUpdater, m.rollouts, m.safetyInterval, m.tuningDrift, m.logger, m.onVIPReconcilerStopped)
			m.vips[key] = recreated
			pendingVIP = pending
		}
	} else if vr, ok := m.vips[key]; ok {
		if vr == stopped {
			delete(m.vips, key)
		} else {
			current = vr
		}
	}
	m.mu.Unlock()

	if recreated != nil {
		go recreated.run(m.ctx)
		recreated.update(pendingVIP)
		return
	}

	// If another reconciler already owns this key, reconcile it after the
	// stopped goroutine exits.
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

	deleteRetryInterval time.Duration

	mu      sync.RWMutex
	current *v1alpha1.VirtualIP
	applied *v1alpha1.VirtualIP
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
		key:                 key,
		dp:                  dp,
		routes:              routes,
		store:               st,
		healthTracker:       ht,
		statusUpdater:       statusUpdater,
		rollouts:            rollouts,
		safetyInterval:      safetyInterval,
		tuningDrift:         normalizeTuningDriftConfig(tuningDrift),
		logger:              logger.With("vip", key),
		eventCh:             make(chan vipEvent, 8),
		reconcileCh:         make(chan struct{}, 1),
		stopCh:              make(chan struct{}),
		stopped:             make(chan struct{}),
		onStopped:           onStopped,
		deleteRetryInterval: defaultDeleteRetryInterval,
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

	var deleteVIP *v1alpha1.VirtualIP
	var deleteRetry <-chan time.Time

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
				deleteVIP = ev.vip
				if vr.handleDelete(ctx, deleteVIP) {
					return // exit goroutine after delete
				}
				deleteRetry = time.After(vr.deleteRetryInterval)
				continue
			}

		case <-deleteRetry:
			if deleteVIP == nil {
				deleteRetry = nil
				continue
			}
			if vr.handleDelete(ctx, deleteVIP) {
				return // exit goroutine after delete
			}
			deleteRetry = time.After(vr.deleteRetryInterval)

		case <-vr.reconcileCh:
			if deleteVIP == nil {
				vr.reconcile(ctx)
			}

		case <-ticker.C:
			if deleteVIP == nil {
				vr.reconcile(ctx)
			}
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

	plan, err := vr.planVIPUpdate(vip)
	if err != nil {
		vr.logger.Warn("failed to plan VIP rollout", "error", err, "generation", vip.Generation)
		span.RecordError(err)
	}

	if vr.rollouts != nil && len(plan.rolloutKeys) > 0 {
		if err := runExclusiveRollouts(ctx, vr.rollouts, plan.rolloutKeys, func(ctx context.Context) error {
			if !vr.isCurrent(vip) {
				vr.logger.Debug("skipping stale VIP rollout", "generation", vip.Generation)
				return nil
			}
			vr.reconcileApplied(ctx, span, vip, healthyBackends, hasHealthy, true, plan.drainBeforeApply)
			return nil
		}); err != nil {
			vr.logger.Error("failed to coordinate VIP rollout", "error", err, "rollouts", plan.rolloutKeys)
			span.RecordError(err)
		}
		return
	}

	vr.reconcileApplied(ctx, span, vip, healthyBackends, hasHealthy, false, plan.drainBeforeApply)
}

type vipUpdatePlan struct {
	rolloutKeys      []string
	drainBeforeApply bool
}

func (vr *vipReconciler) planVIPUpdate(vip *v1alpha1.VirtualIP) (vipUpdatePlan, error) {
	previousAddress, pendingAddressChange := vr.routes.pendingAddressChange(vr.key, vip.Spec.Address)
	if pendingAddressChange {
		return vipUpdatePlan{
			rolloutKeys: rolloutKeysForAddresses(previousAddress, vip.Spec.Address),
		}, nil
	}

	needsDrain, err := vr.needsDrainForVIPUpdate(vip)
	if err != nil {
		return vipUpdatePlan{
			rolloutKeys:      rolloutKeysForAddresses(vip.Spec.Address),
			drainBeforeApply: true,
		}, err
	}
	if !needsDrain {
		return vipUpdatePlan{}, nil
	}
	return vipUpdatePlan{
		rolloutKeys:      rolloutKeysForAddresses(vip.Spec.Address),
		drainBeforeApply: true,
	}, nil
}

func (vr *vipReconciler) needsDrainForVIPUpdate(vip *v1alpha1.VirtualIP) (bool, error) {
	checker, ok := vr.dp.(dataplane.VIPUpdateDrainChecker)
	if !ok {
		return false, nil
	}

	applied, err := vr.lastAppliedVIP(vip)
	if err != nil || applied == nil {
		return false, err
	}
	if applied.Spec.Address != vip.Spec.Address {
		return false, nil
	}
	return checker.NeedsDrainForVIPUpdate(applied, vip)
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
	drainBeforeApply bool,
) {
	if err := vr.persistPendingConfig(vip); err != nil {
		vr.logger.Error("failed to persist pending config before reconcile", "error", err)
		span.RecordError(err)
		if vr.statusUpdater != nil {
			if statusErr := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
				lastConfigPersistFailedCondition(err),
				routeAdvertisedCondition(vip, false, nil),
			); statusErr != nil {
				vr.logger.Warn("failed to update VirtualIP status", "error", statusErr)
				span.RecordError(statusErr)
			}
		}
		return
	}

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

	drainHeld := false
	if drainBeforeApply {
		vr.logger.Info("draining VIP address before disruptive dataplane update", "address", vip.Spec.Address)
		drained, err := vr.routes.BeginAddressDrain(ctx, vr.key, vip.Spec.Address)
		if err != nil {
			vr.logger.Error("failed to drain VIP address route before dataplane update", "error", err)
			span.RecordError(err)
			if vr.statusUpdater != nil {
				if statusErr := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
					servingCondition(vip, len(healthyBackends)),
					routeAdvertisedCondition(vip, false, err),
				); statusErr != nil {
					vr.logger.Warn("failed to update VirtualIP status", "error", statusErr)
					span.RecordError(statusErr)
				}
			}
			return
		}
		if !drained {
			err := fmt.Errorf("VIP address %s is already draining for another VIP", vip.Spec.Address)
			vr.logger.Warn("skipping disruptive dataplane update until VIP address can drain", "address", vip.Spec.Address)
			span.RecordError(err)
			if vr.statusUpdater != nil {
				if statusErr := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
					servingCondition(vip, len(healthyBackends)),
					routeAdvertisedCondition(vip, false, err),
				); statusErr != nil {
					vr.logger.Warn("failed to update VirtualIP status", "error", statusErr)
					span.RecordError(statusErr)
				}
			}
			return
		}
		drainHeld = true
		routeAdvertised = false
		if hasHealthy {
			if err := sleepContext(ctx, normalizeTuningDriftConfig(vr.tuningDrift).DrainDuration); err != nil {
				span.RecordError(err)
				routeAdvertised, routeErr = vr.releaseUpdateDrain(ctx, vip, false)
				if routeErr != nil {
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
		}
	}

	// Apply to data plane
	if err := vr.dp.ApplyVIP(ctx, vip, healthyBackends); err != nil {
		vr.logger.Error("failed to apply VIP to data plane", "error", err)
		span.RecordError(err)
		if drainHeld {
			routeAdvertised, routeErr = vr.releaseUpdateDrain(ctx, vip, false)
		} else {
			routeAdvertised, routeErr = vr.routes.SetServing(ctx, vr.key, vip.Spec.Address, false)
		}
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

	if err := vr.commitLastConfig(vip); err != nil {
		vr.logger.Error("failed to commit last config after dataplane apply", "error", err)
		span.RecordError(err)
		if drainHeld {
			routeAdvertised, routeErr = vr.releaseUpdateDrain(ctx, vip, false)
			if routeErr != nil {
				vr.logger.Error("failed to release VIP address drain after last config commit failure", "error", routeErr)
				span.RecordError(routeErr)
			}
		}
		if vr.statusUpdater != nil {
			if statusErr := vr.statusUpdater.UpdateVIPStatus(ctx, vip, healthyBackends,
				lastConfigPersistFailedCondition(err),
				routeAdvertisedCondition(vip, routeAdvertised, routeErr),
			); statusErr != nil {
				vr.logger.Warn("failed to update VirtualIP status", "error", statusErr)
				span.RecordError(statusErr)
			}
		}
		return
	}
	vr.setLastAppliedVIP(vip)

	if drifts := dataplaneTuningDrifts(vr.dp, vr.key); len(drifts) > 0 {
		repairResult, err := vr.repairTuningDrift(ctx, vip, healthyBackends, hasHealthy, drifts, rolloutHeld)
		if err != nil {
			vr.logger.Error("failed to repair retained VIP tuning drift", "error", err, "drifts", drifts)
			span.RecordError(err)
			if repairResult.routeStateKnown {
				routeAdvertised = repairResult.routeAdvertised
				routeErr = repairResult.routeErr
			}
			if drainHeld {
				routeAdvertised, routeErr = vr.releaseUpdateDrain(ctx, vip, false)
				if routeErr != nil {
					vr.logger.Error("failed to release VIP address drain after tuning drift repair failure", "error", routeErr)
					span.RecordError(routeErr)
				}
			}
			if routeErr != nil {
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
	}

	routeAdvertised, routeErr = vr.routes.SetServing(ctx, vr.key, vip.Spec.Address, hasHealthy)
	if routeErr == nil && drainHeld {
		routeAdvertised, routeErr = vr.routes.FinishDrain(ctx, vr.key, vip.Spec.Address)
	}
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

	vr.logger.Info("VIP reconciled",
		"healthy", len(healthyBackends),
		"total", len(vip.Spec.Backends),
		"serving", hasHealthy,
		"route_advertised", routeAdvertised)
}

func (vr *vipReconciler) persistPendingConfig(vip *v1alpha1.VirtualIP) error {
	if vr.store == nil {
		return nil
	}

	data, err := json.Marshal(vip.Spec)
	if err != nil {
		return fmt.Errorf("failed to encode pending config: %w", err)
	}
	if err := vr.store.SavePendingConfig(vr.key, data); err != nil {
		return fmt.Errorf("failed to save pending config: %w", err)
	}
	return nil
}

func (vr *vipReconciler) commitLastConfig(vip *v1alpha1.VirtualIP) error {
	if vr.store == nil {
		return nil
	}

	data, err := json.Marshal(vip.Spec)
	if err != nil {
		return fmt.Errorf("failed to encode last config: %w", err)
	}
	if err := vr.store.CommitLastConfig(vr.key, data); err != nil {
		return fmt.Errorf("failed to commit last config: %w", err)
	}
	return nil
}

func (vr *vipReconciler) lastAppliedVIP(template *v1alpha1.VirtualIP) (*v1alpha1.VirtualIP, error) {
	vr.mu.RLock()
	applied := vr.applied
	vr.mu.RUnlock()
	if applied != nil {
		return applied.DeepCopy(), nil
	}
	if vr.store == nil {
		return nil, nil
	}

	data, err := vr.store.LoadLastConfig(vr.key)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	var spec v1alpha1.VirtualIPSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to decode last-applied config for %s: %w", vr.key, err)
	}

	vip := template.DeepCopy()
	vip.Spec = spec
	vr.setLastAppliedVIP(vip)
	return vip, nil
}

func (vr *vipReconciler) setLastAppliedVIP(vip *v1alpha1.VirtualIP) {
	vr.mu.Lock()
	defer vr.mu.Unlock()

	if vip == nil {
		vr.applied = nil
		return
	}
	vr.applied = vip.DeepCopy()
}

func (vr *vipReconciler) releaseUpdateDrain(ctx context.Context, vip *v1alpha1.VirtualIP, serving bool) (bool, error) {
	advertised, setErr := vr.routes.SetServing(ctx, vr.key, vip.Spec.Address, serving)
	if setErr != nil {
		return advertised, setErr
	}
	advertised, finishErr := vr.routes.FinishDrain(ctx, vr.key, vip.Spec.Address)
	if finishErr != nil {
		return advertised, finishErr
	}
	return advertised, nil
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

func lastConfigPersistFailedCondition(persistErr error) metav1.Condition {
	message := "Failed to persist last-applied config"
	if persistErr != nil {
		message = persistErr.Error()
	}
	return metav1.Condition{
		Type:    agentstatus.ConditionServing,
		Status:  metav1.ConditionUnknown,
		Reason:  "LastConfigPersistFailed",
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

type tuningDriftRepairResult struct {
	routeAdvertised bool
	routeErr        error
	routeStateKnown bool
}

func (vr *vipReconciler) repairTuningDrift(
	ctx context.Context,
	vip *v1alpha1.VirtualIP,
	healthyBackends []v1alpha1.BackendSpec,
	hasHealthy bool,
	drifts []dataplane.VIPTuningDrift,
	rolloutHeld bool,
) (tuningDriftRepairResult, error) {
	cfg := normalizeTuningDriftConfig(vr.tuningDrift)
	if cfg.Policy == TuningDriftPolicyPreserve {
		vr.logger.Warn("retained VIP tuning drift preserved", "drifts", drifts)
		return tuningDriftRepairResult{}, nil
	}

	if vr.rollouts != nil && !rolloutHeld {
		var result tuningDriftRepairResult
		err := runExclusiveRollouts(ctx, vr.rollouts, rolloutKeysForAddresses(vip.Spec.Address), func(ctx context.Context) error {
			if !vr.isCurrent(vip) {
				vr.logger.Debug("skipping stale retained VIP tuning drift repair", "generation", vip.Generation)
				return nil
			}
			var err error
			result, err = vr.repairTuningDrift(ctx, vip, healthyBackends, hasHealthy, drifts, true)
			return err
		})
		return result, err
	}

	recreator, ok := vr.dp.(dataplane.VIPRecreator)
	if !ok {
		return tuningDriftRepairResult{}, fmt.Errorf("dataplane does not support VIP recreation")
	}

	vr.logger.Info("repairing retained VIP tuning drift", "policy", cfg.Policy, "drain", cfg.DrainDuration, "drifts", drifts)
	drained, err := vr.routes.BeginDrain(ctx, vr.key, vip.Spec.Address)
	if err != nil {
		return tuningDriftRepairResult{
			routeErr:        err,
			routeStateKnown: true,
		}, fmt.Errorf("failed to drain route before VIP recreate: %w", err)
	}
	if !drained {
		vr.logger.Warn("skipping retained VIP tuning drift repair until VIP address can drain", "address", vip.Spec.Address, "drifts", drifts)
		return tuningDriftRepairResult{}, nil
	}
	releaseDrain := func(serving bool, retErr error) (tuningDriftRepairResult, error) {
		advertised, releaseErr := vr.releaseUpdateDrain(ctx, vip, serving)
		result := tuningDriftRepairResult{
			routeAdvertised: advertised,
			routeErr:        releaseErr,
			routeStateKnown: true,
		}
		if releaseErr != nil {
			if retErr != nil {
				return result, fmt.Errorf("%w; additionally failed to release route drain: %v", retErr, releaseErr)
			}
			return result, fmt.Errorf("failed to release route drain after VIP recreate: %w", releaseErr)
		}
		return result, retErr
	}
	if hasHealthy {
		if err := sleepContext(ctx, cfg.DrainDuration); err != nil {
			return releaseDrain(hasHealthy, err)
		}
	}
	if err := recreator.RecreateVIP(ctx, vip, healthyBackends); err != nil {
		return releaseDrain(false, fmt.Errorf("failed to recreate VIP: %w", err))
	}
	advertised, releaseErr := vr.routes.FinishDrain(ctx, vr.key, vip.Spec.Address)
	result := tuningDriftRepairResult{
		routeAdvertised: advertised,
		routeErr:        releaseErr,
		routeStateKnown: true,
	}
	if releaseErr != nil {
		return result, fmt.Errorf("failed to release route drain after VIP recreate: %w", releaseErr)
	}
	return result, nil
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

func (vr *vipReconciler) handleDelete(ctx context.Context, vip *v1alpha1.VirtualIP) bool {
	ctx, span := tracer.Start(ctx, "delete",
		trace.WithAttributes(
			attribute.String("vip.key", vr.key),
			attribute.String("vip.name", vip.Name),
		),
	)
	defer span.End()

	vr.logger.Info("handling VIP deletion")

	cleanupFailed := false

	// Reconcile the shared VIP address route before removing dataplane state.
	if _, err := vr.routes.Delete(ctx, vr.key, vip.Spec.Address); err != nil {
		vr.logger.Error("failed to reconcile route on delete", "error", err)
		span.RecordError(err)
		cleanupFailed = true
	} else if err := vr.dp.RemoveVIP(ctx, vip); err != nil {
		vr.logger.Error("failed to remove VIP from data plane", "error", err)
		span.RecordError(err)
		cleanupFailed = true
	}

	// Clean up local state
	if vr.store != nil {
		if cleanupFailed {
			vr.logger.Warn("preserving local state for delete cleanup retry")
		} else {
			if err := vr.store.DeleteLastConfig(vr.key); err != nil {
				vr.logger.Warn("failed to delete last config", "error", err)
			}
			if err := vr.store.DeletePendingConfig(vr.key); err != nil {
				vr.logger.Warn("failed to delete pending config", "error", err)
			}
			if err := vr.store.DeleteHealthStatesForVIP(vr.key); err != nil {
				vr.logger.Warn("failed to delete health states", "error", err)
			}
		}
	}

	if cleanupFailed {
		vr.logger.Warn("VIP deletion cleanup failed, will retry")
		return false
	}

	vr.logger.Info("VIP deletion handled")
	return true
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
