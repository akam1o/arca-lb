// Package healthcheck provides health probing for VIP backends.
// v2: Persists health state to bbolt, integrates with CRD-based VIPs,
// and fires callbacks on state transitions.
package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("arca-lb/agent/healthcheck")

// V2BackendState represents the health state of a backend.
type V2BackendState string

const (
	V2StateUnknown V2BackendState = "unknown"
	V2StateUp      V2BackendState = "up"
	V2StateDown    V2BackendState = "down"
)

// V2StateChangeCallback is invoked when a backend transitions between states.
// vipKey is the namespaced VirtualIP key ("namespace/name").
type V2StateChangeCallback func(vipKey, backendAddr string, oldState, newState V2BackendState)

// EngineConfig configures the health check engine.
type EngineConfig struct {
	WorkerCount         int
	MaxConcurrentChecks int
	DefaultTimeout      time.Duration
}

// Engine manages health checks for all VIPs.
// It maintains per-backend state with hysteresis (rise/fall counts)
// and persists state to bbolt for crash recovery.
type Engine struct {
	config   EngineConfig
	store    *store.Store
	callback V2StateChangeCallback
	logger   *slog.Logger

	mu      sync.RWMutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc

	// Per-VIP tracking
	vips      map[string]*vipHealthState // key: namespace/name
	nextEpoch uint64

	// Worker pool
	jobCh       chan *probeJob
	resultCh    chan *probeResult
	schedulerWG sync.WaitGroup
	workerWG    sync.WaitGroup
	resultWG    sync.WaitGroup

	// Metrics
	probeCounter  metric.Int64Counter
	probeDuration metric.Float64Histogram
}

type vipHealthState struct {
	vipKey   string
	epoch    uint64
	spec     *v1alpha1.HealthCheckSpec
	backends map[string]*backendHealthState // key: backend address
	prober   V2Prober
	cancel   context.CancelFunc
}

type backendHealthState struct {
	address         string
	targetAddress   string
	state           V2BackendState
	consecutiveUp   int
	consecutiveDown int
	lastProbeTime   time.Time
	lastStateChange time.Time
}

type probeJob struct {
	vipKey      string
	epoch       uint64
	backendAddr string
	targetAddr  string
	prober      V2Prober
	timeout     time.Duration
}

type probeResult struct {
	vipKey      string
	epoch       uint64
	backendAddr string
	success     bool
	latency     time.Duration
	err         error
	timestamp   time.Time
}

// KeyForVIP returns the stable key used by the v2 agent for namespaced VIPs.
func KeyForVIP(vip *v1alpha1.VirtualIP) string {
	if vip.Namespace == "" {
		return vip.Name
	}
	return vip.Namespace + "/" + vip.Name
}

// NewEngine creates a new health check engine.
func NewEngine(cfg EngineConfig, st *store.Store, callback V2StateChangeCallback, logger *slog.Logger) *Engine {
	if cfg.WorkerCount == 0 {
		cfg.WorkerCount = 4
	}
	if cfg.MaxConcurrentChecks == 0 {
		cfg.MaxConcurrentChecks = 64
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 3 * time.Second
	}

	probeCounter, _ := meter.Int64Counter("arca_healthcheck_probes_total",
		metric.WithDescription("Total number of health check probes"))
	probeDuration, _ := meter.Float64Histogram("arca_healthcheck_probe_duration_seconds",
		metric.WithDescription("Duration of health check probes"))

	return &Engine{
		config:        cfg,
		store:         st,
		callback:      callback,
		logger:        logger.With("component", "healthcheck"),
		vips:          make(map[string]*vipHealthState),
		probeCounter:  probeCounter,
		probeDuration: probeDuration,
	}
}

// SetCallback updates the state transition callback.
func (e *Engine) SetCallback(callback V2StateChangeCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callback = callback
}

// Start starts the health check engine.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return fmt.Errorf("health check engine already started")
	}

	e.ctx, e.cancel = context.WithCancel(ctx)
	e.jobCh = make(chan *probeJob, e.config.MaxConcurrentChecks)
	e.resultCh = make(chan *probeResult, e.config.MaxConcurrentChecks)

	// Start workers
	for i := 0; i < e.config.WorkerCount; i++ {
		e.workerWG.Add(1)
		go e.worker(i)
	}

	// Start result processor
	e.resultWG.Add(1)
	go e.processResults()

	// Restore persisted state
	e.restoreState()

	e.started = true
	e.logger.Info("health check engine started", "workers", e.config.WorkerCount)
	return nil
}

// Stop stops the health check engine.
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	e.mu.Unlock()

	e.cancel()

	// Stop all VIP schedulers
	e.mu.Lock()
	for _, vs := range e.vips {
		if vs.cancel != nil {
			vs.cancel()
		}
		if vs.prober != nil {
			if err := vs.prober.Close(); err != nil {
				e.logger.Warn("failed to close prober", "vip", vs.vipKey, "error", err)
			}
		}
	}
	e.vips = make(map[string]*vipHealthState)
	e.mu.Unlock()

	e.schedulerWG.Wait()
	close(e.jobCh)
	e.workerWG.Wait()
	close(e.resultCh)
	e.resultWG.Wait()

	e.logger.Info("health check engine stopped")
}

// StartVIP starts health checking for a VIP.
func (e *Engine) StartVIP(vip *v1alpha1.VirtualIP) error {
	if vip.Spec.HealthCheck == nil {
		return nil
	}
	vipKey := KeyForVIP(vip)

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return fmt.Errorf("health check engine is not started")
	}

	return e.startVIPLocked(vipKey, vip)
}

func (e *Engine) startVIPLocked(vipKey string, vip *v1alpha1.VirtualIP) error {
	prober, err := newProberFromSpec(vip.Spec.HealthCheck)
	if err != nil {
		return fmt.Errorf("failed to create prober for VIP %s: %w", vipKey, err)
	}

	ctx, cancel := context.WithCancel(e.ctx)
	existing := e.vips[vipKey]
	e.nextEpoch++
	vs := &vipHealthState{
		vipKey:   vipKey,
		epoch:    e.nextEpoch,
		spec:     vip.Spec.HealthCheck.DeepCopy(),
		backends: make(map[string]*backendHealthState),
		prober:   prober,
		cancel:   cancel,
	}

	// Initialize backend states (restore from store if available)
	for _, be := range vip.Spec.Backends {
		targetAddress := probeTargetAddress(be)
		bhs := &backendHealthState{
			address:         be.Address,
			targetAddress:   targetAddress,
			state:           V2StateUnknown,
			lastStateChange: time.Now(),
		}

		if existing != nil {
			if current, ok := existing.backends[be.Address]; ok {
				copied := *current
				copied.address = be.Address
				copied.targetAddress = targetAddress
				vs.backends[be.Address] = &copied
				continue
			}
		}

		// Try to restore from persistent store
		if e.store != nil {
			if rec, err := e.store.LoadHealthState(vipKey, be.Address); err == nil && rec != nil {
				bhs.state = V2BackendState(rec.State)
				bhs.consecutiveUp = rec.ConsecutiveUp
				bhs.consecutiveDown = rec.ConsecutiveDown
				bhs.lastProbeTime = rec.LastProbeTime
				bhs.lastStateChange = rec.LastStateChange
				e.logger.Info("restored health state",
					"vip", vipKey, "backend", be.Address, "state", bhs.state)
			}
		}

		vs.backends[be.Address] = bhs
	}

	e.vips[vipKey] = vs
	if existing != nil {
		e.stopVIPState(vipKey, existing)
	}

	// Start scheduler goroutine for this VIP
	e.schedulerWG.Add(1)
	go func() {
		defer e.schedulerWG.Done()
		e.scheduleProbes(ctx, vs)
	}()

	e.logger.Info("started health check for VIP",
		"vip", vipKey, "backends", len(vip.Spec.Backends),
		"interval", vip.Spec.HealthCheck.IntervalSeconds)
	return nil
}

func probeTargetAddress(be v1alpha1.BackendSpec) string {
	if be.MonitorAddress != "" {
		return be.MonitorAddress
	}
	return be.Address
}

// StopVIP stops health checking for a VIP.
func (e *Engine) StopVIP(vipKey string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	vs, ok := e.vips[vipKey]
	if !ok {
		return
	}

	e.stopVIPState(vipKey, vs)
	delete(e.vips, vipKey)

	e.logger.Info("stopped health check for VIP", "vip", vipKey)
}

// UpdateVIP updates health checking for a VIP (backends may have changed).
func (e *Engine) UpdateVIP(vip *v1alpha1.VirtualIP) error {
	if vip.Spec.HealthCheck == nil {
		return nil
	}
	vipKey := KeyForVIP(vip)

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return fmt.Errorf("health check engine is not started")
	}

	return e.startVIPLocked(vipKey, vip)
}

func (e *Engine) stopVIPState(vipKey string, vs *vipHealthState) {
	if vs.cancel != nil {
		vs.cancel()
	}
	if vs.prober != nil {
		if err := vs.prober.Close(); err != nil {
			e.logger.Warn("failed to close prober", "vip", vipKey, "error", err)
		}
	}
}

// IsHealthy returns whether a specific backend is healthy.
func (e *Engine) IsHealthy(vipKey, backendAddr string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipKey]
	if !ok {
		return false
	}
	bhs, ok := vs.backends[backendAddr]
	if !ok {
		return false
	}
	return bhs.state == V2StateUp
}

// HealthyBackends returns the subset of backends that are healthy.
func (e *Engine) HealthyBackends(vipKey string, backends []v1alpha1.BackendSpec) []v1alpha1.BackendSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipKey]
	if !ok {
		return nil
	}

	var healthy []v1alpha1.BackendSpec
	for _, be := range backends {
		if bhs, ok := vs.backends[be.Address]; ok && bhs.state == V2StateUp {
			healthy = append(healthy, be)
		}
	}
	return healthy
}

// GetBackendStates returns the health states for all backends of a VIP.
func (e *Engine) GetBackendStates(vipKey string) map[string]V2BackendState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipKey]
	if !ok {
		return nil
	}

	result := make(map[string]V2BackendState, len(vs.backends))
	for addr, bhs := range vs.backends {
		result[addr] = bhs.state
	}
	return result
}

// --- internal ---

func (e *Engine) scheduleProbes(ctx context.Context, vs *vipHealthState) {
	interval := time.Duration(vs.spec.IntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run first probe immediately
	e.emitProbeJobs(vs)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.emitProbeJobs(vs)
		}
	}
}

func (e *Engine) emitProbeJobs(vs *vipHealthState) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	timeout := time.Duration(vs.spec.TimeoutSeconds) * time.Second
	for addr, bhs := range vs.backends {
		targetAddr := bhs.targetAddress
		if targetAddr == "" {
			targetAddr = addr
		}
		job := &probeJob{
			vipKey:      vs.vipKey,
			epoch:       vs.epoch,
			backendAddr: addr,
			targetAddr:  targetAddr,
			prober:      vs.prober,
			timeout:     timeout,
		}
		select {
		case e.jobCh <- job:
		default:
			e.logger.Warn("job channel full, skipping probe",
				"vip", vs.vipKey, "backend", addr)
		}
	}
}

func (e *Engine) worker(id int) {
	defer e.workerWG.Done()

	for job := range e.jobCh {
		ctx, cancel := context.WithTimeout(e.ctx, job.timeout)
		start := time.Now()
		targetAddr := job.targetAddr
		if targetAddr == "" {
			targetAddr = job.backendAddr
		}
		result := job.prober.Probe(ctx, targetAddr)
		latency := time.Since(start)
		cancel()

		pr := &probeResult{
			vipKey:      job.vipKey,
			epoch:       job.epoch,
			backendAddr: job.backendAddr,
			success:     result.Success,
			latency:     latency,
			err:         result.Error,
			timestamp:   time.Now(),
		}

		// Record metrics
		e.probeCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("vip", job.vipKey),
				attribute.Bool("success", result.Success),
			))
		e.probeDuration.Record(context.Background(), latency.Seconds(),
			metric.WithAttributes(
				attribute.String("vip", job.vipKey),
			))

		select {
		case e.resultCh <- pr:
		default:
			e.logger.Warn("result channel full", "vip", job.vipKey, "backend", job.backendAddr)
		}
	}
}

func (e *Engine) processResults() {
	defer e.resultWG.Done()

	for result := range e.resultCh {
		e.handleResult(result)
	}
}

func (e *Engine) handleResult(result *probeResult) {
	e.mu.Lock()

	vs, ok := e.vips[result.vipKey]
	if !ok {
		e.mu.Unlock()
		return
	}
	if vs.epoch != result.epoch {
		e.mu.Unlock()
		return
	}

	bhs, ok := vs.backends[result.backendAddr]
	if !ok {
		e.mu.Unlock()
		return
	}

	prevState := bhs.state
	bhs.lastProbeTime = result.timestamp

	if result.success {
		bhs.consecutiveUp++
		bhs.consecutiveDown = 0

		if bhs.state != V2StateUp && bhs.consecutiveUp >= vs.spec.RiseCount {
			bhs.state = V2StateUp
			bhs.lastStateChange = result.timestamp
		}
	} else {
		bhs.consecutiveDown++
		bhs.consecutiveUp = 0

		if bhs.state != V2StateDown && bhs.consecutiveDown >= vs.spec.FallCount {
			bhs.state = V2StateDown
			bhs.lastStateChange = result.timestamp
		}
	}

	newState := bhs.state
	stateChanged := prevState != newState
	callback := e.callback
	rec := &store.BackendHealthRecord{
		State:           string(bhs.state),
		ConsecutiveUp:   bhs.consecutiveUp,
		ConsecutiveDown: bhs.consecutiveDown,
		LastProbeTime:   bhs.lastProbeTime,
		LastStateChange: bhs.lastStateChange,
	}

	e.mu.Unlock()

	// Persist state (outside lock)
	if e.store != nil {
		if err := e.store.SaveHealthState(result.vipKey, result.backendAddr, rec); err != nil {
			e.logger.Warn("failed to persist health state",
				"vip", result.vipKey, "backend", result.backendAddr, "error", err)
		}
	}

	// Fire callback on state change
	if stateChanged && callback != nil {
		e.logger.Info("backend state changed",
			"vip", result.vipKey,
			"backend", result.backendAddr,
			"old", prevState,
			"new", newState)
		callback(result.vipKey, result.backendAddr, prevState, newState)
	}
}

func (e *Engine) restoreState() {
	if e.store == nil {
		return
	}

	states, err := e.store.LoadAllHealthStates()
	if err != nil {
		e.logger.Warn("failed to restore health states", "error", err)
		return
	}

	e.logger.Info("restored health states from store", "count", len(states))
}
