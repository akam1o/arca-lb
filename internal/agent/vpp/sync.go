package vpp

import (
	"fmt"
	"sync"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// Syncer implements the VPPSyncer interface for the reconciler
type Syncer struct {
	conn      *Connection
	lbManager *LBManager
	logger    *logrus.Logger

	mu sync.RWMutex
	// Cache of current VPP state
	// This is populated from VPP and kept in sync
	currentVIPs      map[string]*models.VIPConfig
	currentBackends  map[string]*models.Backend
	vipToBackends    map[string][]string
	
	// Metrics (optional)
	metricsRecorder MetricsRecorder
}

// NewSyncer creates a new VPP syncer
func NewSyncer(conn *Connection, lbConfig *config.VPPLBConfig, logger *logrus.Logger) *Syncer {
	lbManager := NewLBManager(conn, lbConfig, logger)

	return &Syncer{
		conn:            conn,
		lbManager:       lbManager,
		logger:          logger,
		currentVIPs:     make(map[string]*models.VIPConfig),
		currentBackends: make(map[string]*models.Backend),
		vipToBackends:   make(map[string][]string),
	}
}

// SetMetricsRecorder sets the metrics recorder for VPP sync metrics
func (s *Syncer) SetMetricsRecorder(recorder MetricsRecorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metricsRecorder = recorder
}

// SyncVIP synchronizes a VIP to VPP
func (s *Syncer) SyncVIP(vipConfig *models.VIPConfig) error {
	s.mu.Lock()

	// Check if VIP already exists
	_, exists := s.currentVIPs[vipConfig.VIP.ID]

	var deleteErr, addErr error

	if exists {
		// VIP exists - this is an update
		// For now, we'll delete and recreate
		// TODO: Implement more efficient update logic
		s.logger.WithField("vip_id", vipConfig.VIP.ID).Debug("VIP exists, recreating")

		// Get existing config for deletion
		existingConfig := s.currentVIPs[vipConfig.VIP.ID]

		// Delete existing VIP
		if err := s.lbManager.DeleteVIPByConfig(existingConfig); err != nil {
			deleteErr = fmt.Errorf("failed to delete existing VIP: %w", err)
		}
	}

	// Add VIP to VPP (only if delete succeeded or VIP didn't exist)
	if deleteErr == nil {
		if err := s.lbManager.AddVIP(vipConfig); err != nil {
			addErr = fmt.Errorf("failed to add VIP to VPP: %w", err)
		}
	}

	// Update cache if operations succeeded
	if deleteErr == nil && addErr == nil {
		s.currentVIPs[vipConfig.VIP.ID] = deepCopyVIPConfig(vipConfig)

		// Update backend cache
		backendIDs := make([]string, 0, len(vipConfig.Backends))
		for i := range vipConfig.Backends {
			backend := &vipConfig.Backends[i]
			s.currentBackends[backend.ID] = deepCopyBackend(backend)
			backendIDs = append(backendIDs, backend.ID)
		}
		s.vipToBackends[vipConfig.VIP.ID] = backendIDs
	}

	s.mu.Unlock()

	// Record metrics after releasing lock
	if deleteErr != nil || addErr != nil {
		s.mu.RLock()
		recorder := s.metricsRecorder
		s.mu.RUnlock()
		if recorder != nil {
			if deleteErr != nil {
				recorder.RecordError("sync", "delete_vip")
			}
			if addErr != nil {
				recorder.RecordError("sync", "add_vip")
			}
		}
	}

	// Return first error encountered
	if deleteErr != nil {
		return deleteErr
	}
	if addErr != nil {
		return addErr
	}

	return nil
}

// DeleteVIP deletes a VIP from VPP
func (s *Syncer) DeleteVIP(vipID string) error {
	s.mu.Lock()

	// Get VIP config from cache
	vipConfig, exists := s.currentVIPs[vipID]
	if !exists {
		s.mu.Unlock()
		s.logger.WithField("vip_id", vipID).Warn("VIP not found in cache, skipping deletion")
		return nil
	}

	// Delete from VPP
	err := s.lbManager.DeleteVIPByConfig(vipConfig)
	s.mu.Unlock()

	// Record metrics after releasing lock
	if err != nil {
		s.mu.RLock()
		recorder := s.metricsRecorder
		s.mu.RUnlock()
		if recorder != nil {
			recorder.RecordError("sync", "delete_vip")
		}
		return fmt.Errorf("failed to delete VIP from VPP: %w", err)
	}

	// Update cache after successful deletion
	s.updateCacheAfterDeleteVIP(vipID)

	return nil
}

// updateCacheAfterDeleteVIP updates cache after successful VIP deletion
func (s *Syncer) updateCacheAfterDeleteVIP(vipID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from cache
	delete(s.currentVIPs, vipID)

	// Remove associated backends from cache
	if backendIDs, ok := s.vipToBackends[vipID]; ok {
		for _, backendID := range backendIDs {
			delete(s.currentBackends, backendID)
		}
		delete(s.vipToBackends, vipID)
	}
}

// AddBackend adds a backend to VPP
func (s *Syncer) AddBackend(vipID string, backend *models.Backend) error {
	s.mu.Lock()

	// Get VIP config
	vipConfig, exists := s.currentVIPs[vipID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("VIP %s not found", vipID)
	}

	// Add backend to VPP
	err := s.lbManager.AddBackendToVIP(vipConfig, backend)
	s.mu.Unlock()

	// Record metrics after releasing lock
	if err != nil {
		s.mu.RLock()
		recorder := s.metricsRecorder
		s.mu.RUnlock()
		if recorder != nil {
			recorder.RecordError("sync", "add_backend")
		}
		return fmt.Errorf("failed to add backend to VPP: %w", err)
	}

	// Update cache after successful addition
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update cache - make a copy to avoid mutation issues
	backendCopy := deepCopyBackend(backend)
	s.currentBackends[backend.ID] = backendCopy

	// Update VIP-backend mapping
	if backendIDs, ok := s.vipToBackends[vipID]; ok {
		s.vipToBackends[vipID] = append(backendIDs, backend.ID)
	} else {
		s.vipToBackends[vipID] = []string{backend.ID}
	}

	// Update VIP config cache to include new backend
	// Make a new slice to avoid concurrent modification issues
	newBackends := make([]models.Backend, len(vipConfig.Backends)+1)
	copy(newBackends, vipConfig.Backends)
	newBackends[len(vipConfig.Backends)] = *backendCopy
	vipConfig.Backends = newBackends

	return nil
}

// DeleteBackend deletes a backend from VPP
func (s *Syncer) DeleteBackend(vipID string, backendID string) error {
	s.mu.Lock()

	// Get VIP config
	vipConfig, exists := s.currentVIPs[vipID]
	if !exists {
		s.mu.Unlock()
		s.logger.WithField("vip_id", vipID).Warn("VIP not found in cache, skipping backend deletion")
		return nil
	}

	// Get backend config
	backend, exists := s.currentBackends[backendID]
	if !exists {
		s.mu.Unlock()
		s.logger.WithField("backend_id", backendID).Warn("Backend not found in cache, skipping deletion")
		return nil
	}

	// Delete from VPP
	err := s.lbManager.DeleteBackendFromVIP(vipConfig, backend)
	s.mu.Unlock()

	// Record metrics after releasing lock
	if err != nil {
		s.mu.RLock()
		recorder := s.metricsRecorder
		s.mu.RUnlock()
		if recorder != nil {
			recorder.RecordError("sync", "delete_backend")
		}
		return fmt.Errorf("failed to delete backend from VPP: %w", err)
	}

	// Update cache after successful deletion
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from cache
	delete(s.currentBackends, backendID)

	// Update VIP-backend mapping
	if backendIDs, ok := s.vipToBackends[vipID]; ok {
		newBackendIDs := make([]string, 0, len(backendIDs))
		for _, bid := range backendIDs {
			if bid != backendID {
				newBackendIDs = append(newBackendIDs, bid)
			}
		}
		s.vipToBackends[vipID] = newBackendIDs
	}

	// Update VIP config cache to remove backend
	newBackends := make([]models.Backend, 0, len(vipConfig.Backends))
	for i := range vipConfig.Backends {
		if vipConfig.Backends[i].ID != backendID {
			newBackends = append(newBackends, vipConfig.Backends[i])
		}
	}
	vipConfig.Backends = newBackends

	return nil
}

// GetCurrentState returns the current VPP state
func (s *Syncer) GetCurrentState() (*models.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build config from cache
	config := &models.Config{
		Revision: 0, // VPP doesn't track revisions
		VIPs:     make([]models.VIPConfig, 0, len(s.currentVIPs)),
	}

	for _, vipConfig := range s.currentVIPs {
		config.VIPs = append(config.VIPs, *deepCopyVIPConfig(vipConfig))
	}

	return config, nil
}

// GetVIPsForMetrics returns VIP information for metrics collection
func (s *Syncer) GetVIPsForMetrics() map[string]VIPInfoForMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vips := make(map[string]VIPInfoForMetrics, len(s.currentVIPs))
	for vipID, vipConfig := range s.currentVIPs {
		vips[vipID] = VIPInfoForMetrics{
			ID:       vipConfig.VIP.ID,
			IP:       vipConfig.VIP.VIP,
			Port:     uint16(vipConfig.VIP.Port),
			Protocol: string(vipConfig.VIP.Protocol),
		}
	}

	return vips
}

// VIPInfoForMetrics contains VIP information for metrics collection
type VIPInfoForMetrics struct {
	ID       string
	IP       string
	Port     uint16
	Protocol string
}

// RefreshState queries VPP for current state and updates cache
//
// CURRENT LIMITATIONS:
//   - This implementation provides basic state verification only
//   - Does NOT actively query VPP for actual configured VIPs/backends
//   - Only verifies internal cache consistency and logs warnings
//
// RATIONALE:
//   - The lb_vip_dump API required for full state queries is not reliably
//     available across all VPP versions in govpp 0.5.0
//   - Current approach avoids API compatibility issues
//
// RECOMMENDED MITIGATIONS:
//   1. Periodic reconciliation: Controller should periodically resend full
//      desired state to re-apply configuration (handles VPP restarts)
//   2. VPP event monitoring: Subscribe to VPP events for state changes
//   3. External monitoring: Deploy separate monitoring to detect VPP restarts
//   4. Health checks: Use health check failures as drift indicators
//
// FUTURE IMPROVEMENTS:
//   - Upgrade to newer govpp version with better LB plugin support
//   - Implement lb_vip_dump parsing when API is stable
//   - Add VPP event notification subscriptions
func (s *Syncer) RefreshState() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Debug("VPP state refresh: verifying cache consistency")

	// Count current cache entries
	vipCount := len(s.currentVIPs)
	backendCount := len(s.currentBackends)

	s.logger.WithFields(map[string]interface{}{
		"vips":     vipCount,
		"backends": backendCount,
	}).Debug("Current cache state")

	// Log warning if cache is empty - may indicate VPP restart or state loss
	if vipCount == 0 && backendCount == 0 {
		s.logger.Warn("VPP state cache is empty - this may indicate VPP restart or state loss")
	}

	return nil
}

// deepCopyVIPConfig creates a deep copy of a VIPConfig
func deepCopyVIPConfig(vipConfig *models.VIPConfig) *models.VIPConfig {
	if vipConfig == nil {
		return nil
	}

	copy := &models.VIPConfig{
		VIP:      vipConfig.VIP,
		Backends: make([]models.Backend, len(vipConfig.Backends)),
	}

	// Deep copy health check if present
	if vipConfig.HealthCheck != nil {
		hcCopy := *vipConfig.HealthCheck
		// Deep copy the Config map if present
		if vipConfig.HealthCheck.Config != nil {
			hcCopy.Config = make(models.HCConfig, len(vipConfig.HealthCheck.Config))
			for k, v := range vipConfig.HealthCheck.Config {
				hcCopy.Config[k] = v
			}
		}
		copy.HealthCheck = &hcCopy
	}

	// Copy backends
	for j, backend := range vipConfig.Backends {
		copy.Backends[j] = backend
	}

	return copy
}

// deepCopyBackend creates a deep copy of a Backend
func deepCopyBackend(backend *models.Backend) *models.Backend {
	if backend == nil {
		return nil
	}

	copy := *backend
	return &copy
}
