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
type V2StateChangeCallback func(vipName, backendAddr string, oldState, newState V2BackendState)

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
	vips map[string]*vipHealthState // key: vip name

	// Worker pool
	jobCh    chan *probeJob
	resultCh chan *probeResult
	wg       sync.WaitGroup

	// Metrics
	probeCounter  metric.Int64Counter
	probeDuration metric.Float64Histogram
}

type vipHealthState struct {
	vipName  string
	spec     *v1alpha1.HealthCheckSpec
	backends map[string]*backendHealthState // key: backend address
	prober   V2Prober
	cancel   context.CancelFunc
}

type backendHealthState struct {
	address         string
	state           V2BackendState
	consecutiveUp   int
	consecutiveDown int
	lastProbeTime   time.Time
	lastStateChange time.Time
}

type probeJob struct {
	vipName     string
	backendAddr string
	prober      V2Prober
	timeout     time.Duration
}

type probeResult struct {
	vipName     string
	backendAddr string
	success     bool
	latency     time.Duration
	err         error
	timestamp   time.Time
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
		e.wg.Add(1)
		go e.worker(i)
	}

	// Start result processor
	e.wg.Add(1)
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
				e.logger.Warn("failed to close prober", "vip", vs.vipName, "error", err)
			}
		}
	}
	e.vips = make(map[string]*vipHealthState)
	e.mu.Unlock()

	close(e.jobCh)
	e.wg.Wait()

	e.logger.Info("health check engine stopped")
}

// StartVIP starts health checking for a VIP.
func (e *Engine) StartVIP(vip *v1alpha1.VirtualIP) error {
	if vip.Spec.HealthCheck == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Stop existing if present
	if vs, ok := e.vips[vip.Name]; ok {
		if vs.cancel != nil {
			vs.cancel()
		}
		if vs.prober != nil {
			if err := vs.prober.Close(); err != nil {
				e.logger.Warn("failed to close prober", "vip", vip.Name, "error", err)
			}
		}
	}

	prober, err := newProberFromSpec(vip.Spec.HealthCheck)
	if err != nil {
		return fmt.Errorf("failed to create prober for VIP %s: %w", vip.Name, err)
	}

	ctx, cancel := context.WithCancel(e.ctx)
	vs := &vipHealthState{
		vipName:  vip.Name,
		spec:     vip.Spec.HealthCheck.DeepCopy(),
		backends: make(map[string]*backendHealthState),
		prober:   prober,
		cancel:   cancel,
	}

	// Initialize backend states (restore from store if available)
	for _, be := range vip.Spec.Backends {
		bhs := &backendHealthState{
			address:         be.Address,
			state:           V2StateUnknown,
			lastStateChange: time.Now(),
		}

		// Try to restore from persistent store
		if e.store != nil {
			if rec, err := e.store.LoadHealthState(vip.Name, be.Address); err == nil && rec != nil {
				bhs.state = V2BackendState(rec.State)
				bhs.consecutiveUp = rec.ConsecutiveUp
				bhs.consecutiveDown = rec.ConsecutiveDown
				bhs.lastProbeTime = rec.LastProbeTime
				bhs.lastStateChange = rec.LastStateChange
				e.logger.Info("restored health state",
					"vip", vip.Name, "backend", be.Address, "state", bhs.state)
			}
		}

		vs.backends[be.Address] = bhs
	}

	e.vips[vip.Name] = vs

	// Start scheduler goroutine for this VIP
	go e.scheduleProbes(ctx, vs)

	e.logger.Info("started health check for VIP",
		"vip", vip.Name, "backends", len(vip.Spec.Backends),
		"interval", vip.Spec.HealthCheck.IntervalSeconds)
	return nil
}

// StopVIP stops health checking for a VIP.
func (e *Engine) StopVIP(vipName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	vs, ok := e.vips[vipName]
	if !ok {
		return
	}

	if vs.cancel != nil {
		vs.cancel()
	}
	if vs.prober != nil {
		if err := vs.prober.Close(); err != nil {
			e.logger.Warn("failed to close prober", "vip", vipName, "error", err)
		}
	}
	delete(e.vips, vipName)

	e.logger.Info("stopped health check for VIP", "vip", vipName)
}

// UpdateVIP updates health checking for a VIP (backends may have changed).
func (e *Engine) UpdateVIP(vip *v1alpha1.VirtualIP) error {
	// Simple approach: stop and restart
	e.StopVIP(vip.Name)
	return e.StartVIP(vip)
}

// IsHealthy returns whether a specific backend is healthy.
func (e *Engine) IsHealthy(vipName, backendAddr string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipName]
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
func (e *Engine) HealthyBackends(vipName string, backends []v1alpha1.BackendSpec) []v1alpha1.BackendSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipName]
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
func (e *Engine) GetBackendStates(vipName string) map[string]V2BackendState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vs, ok := e.vips[vipName]
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
	for addr := range vs.backends {
		job := &probeJob{
			vipName:     vs.vipName,
			backendAddr: addr,
			prober:      vs.prober,
			timeout:     timeout,
		}
		select {
		case e.jobCh <- job:
		default:
			e.logger.Warn("job channel full, skipping probe",
				"vip", vs.vipName, "backend", addr)
		}
	}
}

func (e *Engine) worker(id int) {
	defer e.wg.Done()

	for job := range e.jobCh {
		ctx, cancel := context.WithTimeout(e.ctx, job.timeout)
		start := time.Now()
		result := job.prober.Probe(ctx, job.backendAddr)
		latency := time.Since(start)
		cancel()

		pr := &probeResult{
			vipName:     job.vipName,
			backendAddr: job.backendAddr,
			success:     result.Success,
			latency:     latency,
			err:         result.Error,
			timestamp:   time.Now(),
		}

		// Record metrics
		e.probeCounter.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("vip", job.vipName),
				attribute.Bool("success", result.Success),
			))
		e.probeDuration.Record(context.Background(), latency.Seconds(),
			metric.WithAttributes(
				attribute.String("vip", job.vipName),
			))

		select {
		case e.resultCh <- pr:
		default:
			e.logger.Warn("result channel full", "vip", job.vipName, "backend", job.backendAddr)
		}
	}
}

func (e *Engine) processResults() {
	defer e.wg.Done()

	for result := range e.resultCh {
		e.handleResult(result)
	}
}

func (e *Engine) handleResult(result *probeResult) {
	e.mu.Lock()

	vs, ok := e.vips[result.vipName]
	if !ok {
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

	e.mu.Unlock()

	// Persist state (outside lock)
	if e.store != nil {
		rec := &store.BackendHealthRecord{
			State:           string(bhs.state),
			ConsecutiveUp:   bhs.consecutiveUp,
			ConsecutiveDown: bhs.consecutiveDown,
			LastProbeTime:   bhs.lastProbeTime,
			LastStateChange: bhs.lastStateChange,
		}
		if err := e.store.SaveHealthState(result.vipName, result.backendAddr, rec); err != nil {
			e.logger.Warn("failed to persist health state",
				"vip", result.vipName, "backend", result.backendAddr, "error", err)
		}
	}

	// Fire callback on state change
	if stateChanged && e.callback != nil {
		e.logger.Info("backend state changed",
			"vip", result.vipName,
			"backend", result.backendAddr,
			"old", prevState,
			"new", newState)
		e.callback(result.vipName, result.backendAddr, prevState, newState)
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
