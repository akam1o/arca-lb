package frr

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/sirupsen/logrus"
)

// Manager manages FRR BGP route announcements based on backend health
type Manager struct {
	config *config.FRRConfig
	logger *logrus.Logger
	vtysh  VTYShell

	// VIP health tracking
	vipHealth map[string]*VIPHealth // vipID -> health tracker
	mu        sync.RWMutex

	// Configuration
	routeTag int // Tag for arca-lb routes (default: 10000)
}

// VIPHealth tracks the health status of all backends for a VIP
type VIPHealth struct {
	VIPAddress      string
	HealthyBackends map[string]bool // backendID -> isHealthy
	RouteAnnounced  bool            // Tracks actual BGP route state (for reconciliation)
}

// NewManager creates a new FRR manager
func NewManager(cfg *config.FRRConfig, logger *logrus.Logger) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("FRR config is nil")
	}

	if !cfg.Enabled {
		return nil, fmt.Errorf("FRR integration is disabled in configuration")
	}

	// Validate vtysh binary exists and is accessible
	if _, err := os.Stat(cfg.VTYShell); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("vtysh binary not found at %s", cfg.VTYShell)
		}
		return nil, fmt.Errorf("cannot access vtysh binary at %s: %w", cfg.VTYShell, err)
	}

	// Create vtysh wrapper
	vtysh := NewVTYSh(cfg.VTYShell, logger)

	m := &Manager{
		config:    cfg,
		logger:    logger,
		vtysh:     vtysh,
		vipHealth: make(map[string]*VIPHealth),
		routeTag:  10000, // Default tag for arca-lb routes
	}

	logger.WithFields(logrus.Fields{
		"vtysh_path": cfg.VTYShell,
		"route_tag":  m.routeTag,
	}).Info("FRR manager initialized")

	return m, nil
}

// UpdateBackendHealth updates the health status of a backend and manages BGP route announcements
// Uses two-phase locking: compute action under lock, release lock, execute vtysh, then update state
func (m *Manager) UpdateBackendHealth(vipID, backendID, vipAddress string, oldState, newState healthcheck.BackendState) error {
	m.logger.WithFields(logrus.Fields{
		"vip_id":      vipID,
		"backend_id":  backendID,
		"vip_address": vipAddress,
		"old_state":   oldState,
		"new_state":   newState,
	}).Debug("Updating backend health in FRR manager")

	// Phase 1: Update backend health and determine action under lock
	m.mu.Lock()

	// Initialize VIP health tracker if needed
	if m.vipHealth[vipID] == nil {
		m.vipHealth[vipID] = &VIPHealth{
			VIPAddress:      vipAddress,
			HealthyBackends: make(map[string]bool),
			RouteAnnounced:  false,
		}
		m.logger.WithField("vip_id", vipID).Debug("Initialized VIP health tracker")
	}

	health := m.vipHealth[vipID]

	// Update backend health status
	if newState == healthcheck.StateUp {
		health.HealthyBackends[backendID] = true
	} else {
		health.HealthyBackends[backendID] = false
	}

	// Determine desired route state and current state
	healthyCount := countHealthyBackends(health.HealthyBackends)
	desiredRouteState := healthyCount > 0
	currentRouteState := health.RouteAnnounced

	m.logger.WithFields(logrus.Fields{
		"vip_id":              vipID,
		"healthy_count":       healthyCount,
		"desired_route_state": desiredRouteState,
		"current_route_state": currentRouteState,
	}).Debug("Route state comparison")

	// Release lock before vtysh execution
	m.mu.Unlock()

	// Phase 2: Execute vtysh command WITHOUT holding lock (prevents contention)
	ctx := context.Background()
	var vtyshErr error

	if desiredRouteState && !currentRouteState {
		// Need to announce route
		m.logger.WithFields(logrus.Fields{
			"vip_id":      vipID,
			"vip_address": vipAddress,
		}).Info("Announcing BGP route (backend became healthy)")

		vtyshErr = AddStaticRoute(ctx, m.vtysh, vipAddress, m.routeTag)
		if vtyshErr != nil {
			m.logger.WithError(vtyshErr).Error("Failed to announce BGP route")
			return fmt.Errorf("failed to announce BGP route: %w", vtyshErr)
		}

		// Phase 3: Update route state on success
		// Re-check VIP existence in case it was deleted during vtysh execution
		m.mu.Lock()
		if h, exists := m.vipHealth[vipID]; exists {
			h.RouteAnnounced = true
		}
		m.mu.Unlock()

		m.logger.WithField("vip_address", vipAddress).Info("BGP route announced successfully")

	} else if !desiredRouteState && currentRouteState {
		// Need to withdraw route
		m.logger.WithFields(logrus.Fields{
			"vip_id":      vipID,
			"vip_address": vipAddress,
		}).Warn("Withdrawing BGP route (all backends unhealthy)")

		vtyshErr = DeleteStaticRoute(ctx, m.vtysh, vipAddress, m.routeTag)
		if vtyshErr != nil {
			m.logger.WithError(vtyshErr).Error("Failed to withdraw BGP route")
			return fmt.Errorf("failed to withdraw BGP route: %w", vtyshErr)
		}

		// Phase 3: Update route state on success
		// Re-check VIP existence in case it was deleted during vtysh execution
		m.mu.Lock()
		if h, exists := m.vipHealth[vipID]; exists {
			h.RouteAnnounced = false
		}
		m.mu.Unlock()

		m.logger.WithField("vip_address", vipAddress).Info("BGP route withdrawn successfully")

	} else {
		// No route change needed (route already in desired state)
		m.logger.WithFields(logrus.Fields{
			"vip_id":        vipID,
			"healthy_count": healthyCount,
			"route_state":   currentRouteState,
		}).Debug("No route update needed")
	}

	return nil
}

// StartVIP initializes health tracking for a VIP with its backends
func (m *Manager) StartVIP(vipID, vipAddress string, backendIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"vip_id":      vipID,
		"vip_address": vipAddress,
		"backends":    len(backendIDs),
	}).Info("Starting FRR tracking for VIP")

	// Initialize VIP health tracker
	m.vipHealth[vipID] = &VIPHealth{
		VIPAddress:      vipAddress,
		HealthyBackends: make(map[string]bool),
	}

	// Initialize all backends as unhealthy (false)
	for _, backendID := range backendIDs {
		m.vipHealth[vipID].HealthyBackends[backendID] = false
	}

	return nil
}

// StopVIP removes a VIP from tracking and withdraws its route if announced
// Uses two-phase locking: check state under lock, release lock, execute vtysh, then cleanup
func (m *Manager) StopVIP(vipID string) error {
	m.logger.WithField("vip_id", vipID).Info("Stopping FRR tracking for VIP")

	// Phase 1: Check if route needs withdrawal under lock
	m.mu.Lock()

	health, exists := m.vipHealth[vipID]
	if !exists {
		m.mu.Unlock()
		m.logger.WithField("vip_id", vipID).Debug("VIP not found in FRR tracking")
		return nil
	}

	// Check if route is actually announced
	routeAnnounced := health.RouteAnnounced
	vipAddress := health.VIPAddress

	m.mu.Unlock()

	// Phase 2: Withdraw route WITHOUT holding lock (if needed)
	if routeAnnounced {
		m.logger.WithFields(logrus.Fields{
			"vip_id":      vipID,
			"vip_address": vipAddress,
		}).Info("Withdrawing BGP route for stopped VIP")

		ctx := context.Background()
		if err := DeleteStaticRoute(ctx, m.vtysh, vipAddress, m.routeTag); err != nil {
			m.logger.WithError(err).Warn("Failed to withdraw BGP route for stopped VIP")
			// Continue cleanup despite error
		}
	}

	// Phase 3: Remove VIP from tracking
	m.mu.Lock()
	delete(m.vipHealth, vipID)
	m.mu.Unlock()

	m.logger.WithField("vip_id", vipID).Info("FRR tracking stopped for VIP")

	return nil
}

// Close cleans up FRR manager resources
// Uses two-phase locking: collect routes to withdraw under lock, release lock, execute vtysh, then cleanup
func (m *Manager) Close() error {
	m.logger.Info("Closing FRR manager")

	// Phase 1: Collect all routes that need withdrawal under lock
	m.mu.Lock()

	type routeToWithdraw struct {
		vipID      string
		vipAddress string
	}

	routesToWithdraw := []routeToWithdraw{}
	for vipID, health := range m.vipHealth {
		if health.RouteAnnounced {
			routesToWithdraw = append(routesToWithdraw, routeToWithdraw{
				vipID:      vipID,
				vipAddress: health.VIPAddress,
			})
		}
	}

	m.mu.Unlock()

	// Phase 2: Withdraw routes WITHOUT holding lock
	ctx := context.Background()
	for _, route := range routesToWithdraw {
		m.logger.WithFields(logrus.Fields{
			"vip_id":      route.vipID,
			"vip_address": route.vipAddress,
		}).Info("Withdrawing BGP route during shutdown")

		if err := DeleteStaticRoute(ctx, m.vtysh, route.vipAddress, m.routeTag); err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"vip_id":      route.vipID,
				"vip_address": route.vipAddress,
			}).Warn("Failed to withdraw BGP route during shutdown")
			// Continue with other VIPs
		}
	}

	// Phase 3: Clear tracking map
	m.mu.Lock()
	m.vipHealth = make(map[string]*VIPHealth)
	m.mu.Unlock()

	m.logger.Info("FRR manager closed")

	return nil
}

// GetVIPHealth returns the health status for a VIP (for debugging/monitoring)
func (m *Manager) GetVIPHealth(vipID string) (vipAddress string, healthyCount int, totalCount int, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	health, exists := m.vipHealth[vipID]
	if !exists {
		return "", 0, 0, false
	}

	healthyCount = countHealthyBackends(health.HealthyBackends)
	totalCount = len(health.HealthyBackends)

	return health.VIPAddress, healthyCount, totalCount, true
}

// countHealthyBackends counts the number of healthy backends
func countHealthyBackends(backends map[string]bool) int {
	count := 0
	for _, isHealthy := range backends {
		if isHealthy {
			count++
		}
	}
	return count
}
