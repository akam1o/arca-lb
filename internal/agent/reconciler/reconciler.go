package reconciler

import (
	"context"
	"sync"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/state"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// VPPSyncer defines the interface for VPP synchronization
type VPPSyncer interface {
	// SyncVIP synchronizes a VIP to VPP
	SyncVIP(vipConfig *models.VIPConfig) error

	// DeleteVIP deletes a VIP from VPP
	DeleteVIP(vipID string) error

	// AddBackend adds a backend to VPP
	AddBackend(vipID string, backend *models.Backend) error

	// DeleteBackend deletes a backend from VPP
	DeleteBackend(vipID string, backendID string) error

	// GetCurrentState returns the current VPP state
	GetCurrentState() (*models.Config, error)
}

// HealthCheckManager defines the interface for health check management
type HealthCheckManager interface {
	// StartHealthCheck starts health checking for a VIP
	StartHealthCheck(vipConfig *models.VIPConfig) error

	// StopHealthCheck stops health checking for a VIP
	StopHealthCheck(vipID string) error

	// UpdateHealthCheck updates health check configuration
	UpdateHealthCheck(vipConfig *models.VIPConfig) error
}

// MetricsRecorder is an interface for recording reconciler metrics
type MetricsRecorder interface {
	RecordReconcile(result string)
}

// Reconciler is responsible for reconciling the desired state with the actual state
type Reconciler struct {
	config        *config.Config
	logger        *logrus.Logger
	stateManager  *state.Manager
	vppSyncer     VPPSyncer
	healthChecker HealthCheckManager

	mu          sync.Mutex
	started     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
	reconcileCh chan struct{}
	
	// Metrics (optional)
	metricsRecorder MetricsRecorder
}

// NewReconciler creates a new reconciler
func NewReconciler(
	cfg *config.Config,
	logger *logrus.Logger,
	stateManager *state.Manager,
	vppSyncer VPPSyncer,
	healthChecker HealthCheckManager,
) *Reconciler {
	return &Reconciler{
		config:        cfg,
		logger:        logger,
		stateManager:  stateManager,
		vppSyncer:     vppSyncer,
		healthChecker: healthChecker,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
		reconcileCh:   make(chan struct{}, 1),
	}
}

// SetMetricsRecorder sets the metrics recorder for reconciler metrics
func (r *Reconciler) SetMetricsRecorder(recorder MetricsRecorder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metricsRecorder = recorder
}

// Start starts the reconciliation loop
func (r *Reconciler) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		r.logger.Warn("Reconciler already started")
		return
	}
	r.started = true
	// Reinitialize channels for restartability
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.reconcileCh = make(chan struct{}, 1)
	r.mu.Unlock()

	r.logger.Info("Starting reconciliation loop")

	go r.reconcileLoop(ctx)
}

// Stop stops the reconciliation loop
func (r *Reconciler) Stop() {
	r.logger.Info("Stopping reconciliation loop")

	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		r.logger.Debug("Reconciler not started, nothing to stop")
		return
	}
	r.mu.Unlock()

	// Close stopCh only once (idempotent)
	select {
	case <-r.stopCh:
		// Already stopped, wait for completion
		<-r.doneCh
		return
	default:
		close(r.stopCh)
	}

	<-r.doneCh

	// Reset started flag for restartability
	r.mu.Lock()
	r.started = false
	r.mu.Unlock()
}

// TriggerReconcile triggers an immediate reconciliation
func (r *Reconciler) TriggerReconcile() {
	select {
	case r.reconcileCh <- struct{}{}:
		r.logger.Debug("Reconciliation triggered")
	default:
		r.logger.Debug("Reconciliation already pending")
	}
}

// reconcileLoop is the main reconciliation loop
func (r *Reconciler) reconcileLoop(ctx context.Context) {
	defer close(r.doneCh)

	ticker := time.NewTicker(r.config.Agent.ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile()
		case <-r.reconcileCh:
			r.reconcile()
		}
	}
}

// reconcile performs a reconciliation cycle
func (r *Reconciler) reconcile() {
	r.mu.Lock()
	// Get metrics recorder before releasing lock
	recorder := r.metricsRecorder
	r.mu.Unlock()

	r.logger.Debug("Starting reconciliation")

	// Get desired state
	desiredConfig := r.stateManager.GetConfig()
	if desiredConfig == nil {
		r.logger.Debug("No desired configuration, skipping reconciliation")
		if recorder != nil {
			recorder.RecordReconcile("no_change")
		}
		return
	}

	// Get current VPP state
	currentConfig, err := r.vppSyncer.GetCurrentState()
	if err != nil {
		r.logger.WithError(err).Error("Failed to get current VPP state")
		if recorder != nil {
			recorder.RecordReconcile("failure")
		}
		return
	}

	// Compute diff
	tempManager := state.NewManager()
	tempManager.UpdateConfig(currentConfig)
	diff := tempManager.ComputeDiff(desiredConfig)

	if diff.IsEmpty() {
		r.logger.Debug("No changes detected, configuration is in sync")
		if recorder != nil {
			recorder.RecordReconcile("no_change")
		}
		return
	}

	r.logger.WithFields(logrus.Fields{
		"summary": diff.Summary(),
	}).Info("Configuration drift detected, reconciling")

	// Apply changes
	if err := r.applyChanges(diff); err != nil {
		r.logger.WithError(err).Error("Failed to apply configuration changes")
		if recorder != nil {
			recorder.RecordReconcile("failure")
		}
		return
	}

	r.logger.Info("Reconciliation completed successfully")
	if recorder != nil {
		recorder.RecordReconcile("success")
	}
}

// applyChanges applies the configuration changes
func (r *Reconciler) applyChanges(diff *state.ConfigDiff) error {
	// Stop health checks for removed VIPs first
	for _, vipConfig := range diff.RemovedVIPs {
		if r.healthChecker != nil && vipConfig.HealthCheck != nil {
			r.logger.WithField("vip_id", vipConfig.VIP.ID).Info("Stopping health check for VIP")
			if err := r.healthChecker.StopHealthCheck(vipConfig.VIP.ID); err != nil {
				r.logger.WithError(err).Warn("Failed to stop health check")
			}
		}
	}

	// Remove backends before removing VIPs
	for _, backend := range diff.RemovedBackends {
		r.logger.WithFields(logrus.Fields{
			"backend_id": backend.ID,
			"vip_id":     backend.VIPID,
		}).Info("Removing backend")

		if err := r.vppSyncer.DeleteBackend(backend.VIPID, backend.ID); err != nil {
			r.logger.WithError(err).Error("Failed to delete backend from VPP")
			return err
		}
	}

	// Now remove VIPs after backends are deleted
	for _, vipConfig := range diff.RemovedVIPs {
		r.logger.WithField("vip_id", vipConfig.VIP.ID).Info("Removing VIP")

		// Delete from VPP
		if err := r.vppSyncer.DeleteVIP(vipConfig.VIP.ID); err != nil {
			r.logger.WithError(err).Error("Failed to delete VIP from VPP")
			return err
		}
	}

	// Add new VIPs
	for _, vipConfig := range diff.AddedVIPs {
		// Create a copy to avoid range variable pointer issue
		vipConfigCopy := vipConfig

		r.logger.WithFields(logrus.Fields{
			"vip_id":   vipConfigCopy.VIP.ID,
			"vip":      vipConfigCopy.VIP.VIP,
			"port":     vipConfigCopy.VIP.Port,
			"protocol": vipConfigCopy.VIP.Protocol,
		}).Info("Adding VIP")

		// If health checks are configured, create VIP without backends
		// Health check system will add backends as they become healthy
		configToSync := vipConfigCopy
		if vipConfigCopy.HealthCheck != nil {
			r.logger.WithField("vip_id", vipConfigCopy.VIP.ID).
				Info("VIP has health checks configured, backends will be added by health check system")

			// Create a copy with empty backends for initial sync
			configToSync = models.VIPConfig{
				VIP:         vipConfigCopy.VIP,
				HealthCheck: vipConfigCopy.HealthCheck,
				Backends:    []models.Backend{}, // Empty - health checks will add them
			}
		}

		if err := r.vppSyncer.SyncVIP(&configToSync); err != nil {
			r.logger.WithError(err).Error("Failed to add VIP to VPP")
			return err
		}

		// Start health checks if configured (with original backend list)
		if r.healthChecker != nil && vipConfigCopy.HealthCheck != nil {
			if err := r.healthChecker.StartHealthCheck(&vipConfigCopy); err != nil {
				r.logger.WithError(err).Warn("Failed to start health check")
			}
		}
	}

	// Update modified VIPs
	for _, vipConfig := range diff.ModifiedVIPs {
		// Create a copy to avoid range variable pointer issue
		vipConfigCopy := vipConfig

		r.logger.WithFields(logrus.Fields{
			"vip_id": vipConfigCopy.VIP.ID,
		}).Info("Updating VIP")

		if err := r.vppSyncer.SyncVIP(&vipConfigCopy); err != nil {
			r.logger.WithError(err).Error("Failed to update VIP in VPP")
			return err
		}

		// Update health checks if configured
		if r.healthChecker != nil && vipConfigCopy.HealthCheck != nil {
			if err := r.healthChecker.UpdateHealthCheck(&vipConfigCopy); err != nil {
				r.logger.WithError(err).Warn("Failed to update health check")
			}
		}
	}

	// Add new backends
	for _, backend := range diff.AddedBackends {
		// Check if VIP has health checks configured
		// If so, skip adding backend - health check system will handle it
		vipHealthCheck := r.stateManager.GetHealthCheck(backend.VIPID)
		if vipHealthCheck != nil {
			r.logger.WithFields(logrus.Fields{
				"backend_id": backend.ID,
				"vip_id":     backend.VIPID,
			}).Debug("Skipping backend add - VIP has health checks configured")
			continue
		}

		r.logger.WithFields(logrus.Fields{
			"backend_id": backend.ID,
			"vip_id":     backend.VIPID,
			"ip":         backend.IP,
			"weight":     backend.Weight,
		}).Info("Adding backend")

		if err := r.vppSyncer.AddBackend(backend.VIPID, &backend); err != nil {
			r.logger.WithError(err).Error("Failed to add backend to VPP")
			return err
		}
	}

	return nil
}

// OnConfigChange handles configuration changes from the controller
func (r *Reconciler) OnConfigChange(config *models.Config) error {
	r.logger.WithFields(logrus.Fields{
		"revision":  config.Revision,
		"vip_count": len(config.VIPs),
	}).Info("Processing configuration change")

	// Update state manager
	r.stateManager.UpdateConfig(config)

	// Trigger immediate reconciliation
	r.TriggerReconcile()

	return nil
}
