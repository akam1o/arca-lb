package healthcheck

import (
	"context"
	"fmt"
	"sync"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// MetricsRecorder is an interface for recording health check metrics
type MetricsRecorder interface {
	RecordProbeResult(vipID, backendID string, success bool, duration float64)
}

// Manager manages health checking for all VIPs
type Manager struct {
	config   *config.HealthCheckConfig
	logger   *logrus.Logger
	callback StateChangeCallback

	mu      sync.RWMutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc

	// Worker pool
	workers  []*Worker
	jobCh    chan *ProbeJob
	resultCh chan *ProbeJobResult

	// State tracking
	stateTracker *StateTracker

	// Job scheduling
	scheduler *JobScheduler

	// VIP management
	vipProbers map[string]Prober            // vipID -> Prober
	vipConfigs map[string]*models.VIPConfig // vipID -> VIPConfig

	// Metrics (optional)
	metricsRecorder MetricsRecorder

	// Result processor
	wg sync.WaitGroup

	// Worker pool wait group
	workerWg sync.WaitGroup
}

// NewManager creates a new health check manager
func NewManager(
	cfg *config.HealthCheckConfig,
	logger *logrus.Logger,
	callback StateChangeCallback,
) *Manager {
	return &Manager{
		config:       cfg,
		logger:       logger,
		callback:     callback,
		stateTracker: NewStateTracker(logger),
		vipProbers:   make(map[string]Prober),
		vipConfigs:   make(map[string]*models.VIPConfig),
	}
}

// SetMetricsRecorder sets the metrics recorder for health check metrics
func (m *Manager) SetMetricsRecorder(recorder MetricsRecorder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metricsRecorder = recorder
}

// GetStateTracker returns the state tracker (for metrics collection)
func (m *Manager) GetStateTracker() *StateTracker {
	return m.stateTracker
}

// Start starts the health check manager
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("health check manager already started")
	}

	m.logger.Info("Starting health check manager")

	// Create context
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Create channels
	m.jobCh = make(chan *ProbeJob, m.config.MaxConcurrentChecks)
	m.resultCh = make(chan *ProbeJobResult, m.config.MaxConcurrentChecks)

	// Create job scheduler
	m.scheduler = NewJobScheduler(m.jobCh, m.logger)

	// Start worker pool
	m.workers = make([]*Worker, m.config.WorkerCount)
	for i := 0; i < m.config.WorkerCount; i++ {
		worker := NewWorker(i, m.jobCh, m.resultCh, m.logger)
		m.workers[i] = worker
		m.workerWg.Add(1)
		go func(w *Worker) {
			defer m.workerWg.Done()
			w.Start(m.ctx)
		}(worker)
	}

	// Start result processor
	m.wg.Add(1)
	go m.processResults()

	m.started = true

	m.logger.WithFields(logrus.Fields{
		"worker_count":   m.config.WorkerCount,
		"max_concurrent": m.config.MaxConcurrentChecks,
	}).Info("Health check manager started")

	return nil
}

// Stop stops the health check manager
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		m.logger.Debug("Health check manager not started, nothing to stop")
		return
	}
	m.mu.Unlock()

	m.logger.Info("Stopping health check manager")

	// Step 1: Stop scheduler (stops emitting new jobs)
	m.scheduler.Stop()

	// Step 2: Cancel context (interrupts in-flight probes)
	m.cancel()

	// Step 3: Close job channel (signals workers to exit)
	close(m.jobCh)

	// Step 4: Wait for all workers to finish
	m.workerWg.Wait()

	// Step 5: Close result channel (now safe - no workers sending)
	close(m.resultCh)

	// Step 6: Wait for result processor to finish
	m.wg.Wait()

	// Close all probers
	m.mu.Lock()
	for vipID, prober := range m.vipProbers {
		if err := prober.Close(); err != nil {
			m.logger.WithFields(logrus.Fields{
				"vip_id": vipID,
				"error":  err,
			}).Error("Failed to close prober")
		}
	}
	m.vipProbers = make(map[string]Prober)
	m.vipConfigs = make(map[string]*models.VIPConfig)
	m.started = false
	m.mu.Unlock()

	m.logger.Info("Health check manager stopped")
}

// StartHealthCheck starts health checking for a VIP
func (m *Manager) StartHealthCheck(vipConfig *models.VIPConfig) error {
	if vipConfig.HealthCheck == nil {
		m.logger.WithField("vip_id", vipConfig.VIP.ID).Debug("No health check configured for VIP")
		return nil
	}

	// Validate health check configuration
	hc := vipConfig.HealthCheck
	if err := models.ValidateHealthCheckTiming(hc); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return fmt.Errorf("health check manager not started")
	}

	vipID := vipConfig.VIP.ID

	// Stop existing health check if any
	if existing, ok := m.vipProbers[vipID]; ok {
		m.logger.WithField("vip_id", vipID).Debug("Stopping existing health check for VIP")
		m.scheduler.StopVIP(vipID)
		if err := existing.Close(); err != nil {
			m.logger.WithError(err).Warn("Failed to close existing prober")
		}
		delete(m.vipProbers, vipID)
		delete(m.vipConfigs, vipID)
		m.stateTracker.RemoveVIP(vipID)
	}

	// Create prober
	prober, err := NewProber(vipConfig.HealthCheck, m.logger)
	if err != nil {
		return fmt.Errorf("failed to create prober: %w", err)
	}

	// Store prober and deep copy of config to prevent races
	m.vipProbers[vipID] = prober
	m.vipConfigs[vipID] = deepCopyVIPConfigForHealthCheck(vipConfig)

	// Start scheduler with deep copy
	vipConfigCopy := deepCopyVIPConfigForHealthCheck(vipConfig)
	if err := m.scheduler.StartVIP(m.ctx, vipConfigCopy, prober); err != nil {
		// Clean up prober on failure
		if closeErr := prober.Close(); closeErr != nil {
			m.logger.WithError(closeErr).Warn("Failed to close prober after scheduler start failure")
		}
		delete(m.vipProbers, vipID)
		delete(m.vipConfigs, vipID)
		return fmt.Errorf("failed to start scheduler: %w", err)
	}

	m.logger.WithFields(logrus.Fields{
		"vip_id":       vipID,
		"type":         vipConfig.HealthCheck.Type,
		"interval_sec": vipConfig.HealthCheck.IntervalSec,
		"rise_count":   vipConfig.HealthCheck.RiseCount,
		"fall_count":   vipConfig.HealthCheck.FallCount,
		"backends":     len(vipConfig.Backends),
	}).Info("Started health check for VIP")

	return nil
}

// StopHealthCheck stops health checking for a VIP
func (m *Manager) StopHealthCheck(vipID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return fmt.Errorf("health check manager not started")
	}

	// Stop scheduler
	m.scheduler.StopVIP(vipID)

	// Close prober
	if prober, ok := m.vipProbers[vipID]; ok {
		if err := prober.Close(); err != nil {
			m.logger.WithError(err).Warn("Failed to close prober")
		}
		delete(m.vipProbers, vipID)
	}

	// Remove config
	delete(m.vipConfigs, vipID)

	// Remove state
	m.stateTracker.RemoveVIP(vipID)

	m.logger.WithField("vip_id", vipID).Info("Stopped health check for VIP")

	return nil
}

// UpdateHealthCheck updates health checking for a VIP
func (m *Manager) UpdateHealthCheck(vipConfig *models.VIPConfig) error {
	// For now, just stop and start
	// This ensures clean state and prober recreation
	vipID := vipConfig.VIP.ID

	if err := m.StopHealthCheck(vipID); err != nil {
		return fmt.Errorf("failed to stop existing health check: %w", err)
	}

	if err := m.StartHealthCheck(vipConfig); err != nil {
		return fmt.Errorf("failed to start updated health check: %w", err)
	}

	return nil
}

// processResults processes health check results from workers
func (m *Manager) processResults() {
	defer m.wg.Done()

	m.logger.Debug("Result processor started")

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debug("Result processor stopped (context done)")
			return

		case result, ok := <-m.resultCh:
			if !ok {
				m.logger.Debug("Result processor stopped (result channel closed)")
				return
			}

			// Process result
			m.handleResult(result)
		}
	}
}

// handleResult handles a single health check result
func (m *Manager) handleResult(jobResult *ProbeJobResult) {
	// Record metrics if recorder is available
	m.mu.RLock()
	recorder := m.metricsRecorder
	m.mu.RUnlock()

	if recorder != nil {
		duration := jobResult.Result.Latency.Seconds()
		recorder.RecordProbeResult(
			jobResult.VIPID,
			jobResult.BackendID,
			jobResult.Result.Success,
			duration,
		)
	}

	// Update state tracker
	prevState, newState, stateChanged := m.stateTracker.UpdateProbeResult(
		jobResult.VIPID,
		jobResult.BackendID,
		jobResult.Result,
		jobResult.RiseCount,
		jobResult.FallCount,
	)

	// If state changed, call callback
	if stateChanged && m.callback != nil {
		// Get backend from VIP config
		m.mu.RLock()
		vipConfig, exists := m.vipConfigs[jobResult.VIPID]
		m.mu.RUnlock()

		if !exists {
			m.logger.WithField("vip_id", jobResult.VIPID).Warn("VIP config not found for state change callback")
			return
		}

		// Find backend
		var backend *models.Backend
		for i := range vipConfig.Backends {
			if vipConfig.Backends[i].ID == jobResult.BackendID {
				backend = &vipConfig.Backends[i]
				break
			}
		}

		if backend == nil {
			m.logger.WithFields(logrus.Fields{
				"vip_id":     jobResult.VIPID,
				"backend_id": jobResult.BackendID,
			}).Warn("Backend not found for state change callback")
			return
		}

		// Extract VIP address for callback (needed for FRR BGP route management)
		vipAddress := vipConfig.VIP.VIP

		// Call callback with VIP address
		m.callback(jobResult.VIPID, jobResult.BackendID, prevState, newState, backend, vipAddress)
	}
}

// deepCopyVIPConfigForHealthCheck creates a deep copy of VIPConfig for health check use
func deepCopyVIPConfigForHealthCheck(vipConfig *models.VIPConfig) *models.VIPConfig {
	if vipConfig == nil {
		return nil
	}

	result := &models.VIPConfig{
		VIP: vipConfig.VIP,
	}

	// Deep copy HealthCheck if present
	if vipConfig.HealthCheck != nil {
		hcCopy := *vipConfig.HealthCheck
		// Deep copy the Config map if present
		if vipConfig.HealthCheck.Config != nil {
			hcCopy.Config = make(models.HCConfig, len(vipConfig.HealthCheck.Config))
			for k, v := range vipConfig.HealthCheck.Config {
				hcCopy.Config[k] = v
			}
		}
		result.HealthCheck = &hcCopy
	}

	// Deep copy Backends slice
	if len(vipConfig.Backends) > 0 {
		result.Backends = make([]models.Backend, len(vipConfig.Backends))
		copy(result.Backends, vipConfig.Backends)
	}

	return result
}
