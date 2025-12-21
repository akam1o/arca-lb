package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/state"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// Mock VPP Syncer
type mockVPPSyncer struct {
	syncedVIPs      []*models.VIPConfig
	deletedVIPs     []string
	addedBackends   map[string][]*models.Backend
	deletedBackends map[string][]string
	currentState    *models.Config
	syncErr         error
}

func newMockVPPSyncer() *mockVPPSyncer {
	return &mockVPPSyncer{
		syncedVIPs:      make([]*models.VIPConfig, 0),
		deletedVIPs:     make([]string, 0),
		addedBackends:   make(map[string][]*models.Backend),
		deletedBackends: make(map[string][]string),
		currentState: &models.Config{
			Revision: 0,
			VIPs:     []models.VIPConfig{},
		},
	}
}

func (m *mockVPPSyncer) SyncVIP(vipConfig *models.VIPConfig) error {
	if m.syncErr != nil {
		return m.syncErr
	}
	m.syncedVIPs = append(m.syncedVIPs, vipConfig)

	// Update current state to reflect the sync
	found := false
	for i := range m.currentState.VIPs {
		if m.currentState.VIPs[i].VIP.ID == vipConfig.VIP.ID {
			m.currentState.VIPs[i] = *vipConfig
			found = true
			break
		}
	}
	if !found {
		m.currentState.VIPs = append(m.currentState.VIPs, *vipConfig)
	}

	return nil
}

func (m *mockVPPSyncer) DeleteVIP(vipID string) error {
	if m.syncErr != nil {
		return m.syncErr
	}
	m.deletedVIPs = append(m.deletedVIPs, vipID)

	// Update current state to reflect the deletion
	newVIPs := make([]models.VIPConfig, 0)
	for i := range m.currentState.VIPs {
		if m.currentState.VIPs[i].VIP.ID != vipID {
			newVIPs = append(newVIPs, m.currentState.VIPs[i])
		}
	}
	m.currentState.VIPs = newVIPs

	return nil
}

func (m *mockVPPSyncer) AddBackend(vipID string, backend *models.Backend) error {
	if m.syncErr != nil {
		return m.syncErr
	}
	if m.addedBackends[vipID] == nil {
		m.addedBackends[vipID] = make([]*models.Backend, 0)
	}
	m.addedBackends[vipID] = append(m.addedBackends[vipID], backend)

	// Update current state to reflect the backend addition
	for i := range m.currentState.VIPs {
		if m.currentState.VIPs[i].VIP.ID == vipID {
			// Check if backend already exists
			found := false
			for j := range m.currentState.VIPs[i].Backends {
				if m.currentState.VIPs[i].Backends[j].ID == backend.ID {
					m.currentState.VIPs[i].Backends[j] = *backend
					found = true
					break
				}
			}
			if !found {
				m.currentState.VIPs[i].Backends = append(m.currentState.VIPs[i].Backends, *backend)
			}
			break
		}
	}

	return nil
}

func (m *mockVPPSyncer) DeleteBackend(vipID string, backendID string) error {
	if m.syncErr != nil {
		return m.syncErr
	}
	if m.deletedBackends[vipID] == nil {
		m.deletedBackends[vipID] = make([]string, 0)
	}
	m.deletedBackends[vipID] = append(m.deletedBackends[vipID], backendID)

	// Update current state to reflect the backend deletion
	for i := range m.currentState.VIPs {
		if m.currentState.VIPs[i].VIP.ID == vipID {
			newBackends := make([]models.Backend, 0)
			for j := range m.currentState.VIPs[i].Backends {
				if m.currentState.VIPs[i].Backends[j].ID != backendID {
					newBackends = append(newBackends, m.currentState.VIPs[i].Backends[j])
				}
			}
			m.currentState.VIPs[i].Backends = newBackends
			break
		}
	}

	return nil
}

func (m *mockVPPSyncer) GetCurrentState() (*models.Config, error) {
	if m.syncErr != nil {
		return nil, m.syncErr
	}
	return m.currentState, nil
}

// Mock Health Check Manager
type mockHealthCheckManager struct {
	started []string
	stopped []string
	updated []string
}

func newMockHealthCheckManager() *mockHealthCheckManager {
	return &mockHealthCheckManager{
		started: make([]string, 0),
		stopped: make([]string, 0),
		updated: make([]string, 0),
	}
}

func (m *mockHealthCheckManager) StartHealthCheck(vipConfig *models.VIPConfig) error {
	m.started = append(m.started, vipConfig.VIP.ID)
	return nil
}

func (m *mockHealthCheckManager) StopHealthCheck(vipID string) error {
	m.stopped = append(m.stopped, vipID)
	return nil
}

func (m *mockHealthCheckManager) UpdateHealthCheck(vipConfig *models.VIPConfig) error {
	m.updated = append(m.updated, vipConfig.VIP.ID)
	return nil
}

func TestNewReconciler(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 10 * time.Second,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel) // Suppress logs during tests

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	if r == nil {
		t.Fatal("NewReconciler returned nil")
	}
}

func TestOnConfigChange(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 10 * time.Second,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	newConfig := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
		},
	}

	err := r.OnConfigChange(newConfig)
	if err != nil {
		t.Errorf("OnConfigChange returned error: %v", err)
	}

	// Verify state was updated
	currentConfig := stateManager.GetConfig()
	if currentConfig.Revision != 1 {
		t.Errorf("Expected revision 1, got %d", currentConfig.Revision)
	}
}

func TestReconcileAddVIP(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 100 * time.Millisecond,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	// Start reconciler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Set desired config
	newConfig := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				HealthCheck: &models.HealthCheck{
					ID:          "hc1",
					VIPID:       "vip1",
					Type:        models.HCTypeHTTP,
					IntervalSec: 5,
				},
			},
		},
	}

	if err := r.OnConfigChange(newConfig); err != nil {
		t.Fatalf("OnConfigChange returned error: %v", err)
	}

	// Wait for reconciliation
	time.Sleep(200 * time.Millisecond)

	r.Stop()

	// Verify VIP was synced
	if len(vppSyncer.syncedVIPs) != 1 {
		t.Errorf("Expected 1 synced VIP, got %d", len(vppSyncer.syncedVIPs))
	}

	// Verify health check was started
	if len(healthChecker.started) != 1 {
		t.Errorf("Expected 1 started health check, got %d", len(healthChecker.started))
	}
}

func TestReconcileDeleteVIP(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 100 * time.Millisecond,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	// Set current VPP state with a VIP
	vppSyncer.currentState = &models.Config{
		Revision: 0,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
		},
	}

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	// Start reconciler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Set empty desired config (remove all VIPs)
	emptyConfig := &models.Config{
		Revision: 1,
		VIPs:     []models.VIPConfig{},
	}

	if err := r.OnConfigChange(emptyConfig); err != nil {
		t.Fatalf("OnConfigChange returned error: %v", err)
	}

	// Wait for reconciliation
	time.Sleep(200 * time.Millisecond)

	r.Stop()

	// Verify VIP was deleted
	if len(vppSyncer.deletedVIPs) != 1 {
		t.Errorf("Expected 1 deleted VIP, got %d", len(vppSyncer.deletedVIPs))
	}

	if vppSyncer.deletedVIPs[0] != "vip1" {
		t.Errorf("Expected deleted VIP 'vip1', got '%s'", vppSyncer.deletedVIPs[0])
	}
}

func TestReconcileAddBackend(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 100 * time.Millisecond,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	// Set current VPP state with a VIP but no backends
	vppSyncer.currentState = &models.Config{
		Revision: 0,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{},
			},
		},
	}

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	// Start reconciler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Set desired config with backend
	newConfig := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend1",
						VIPID:  "vip1",
						IP:     "192.168.1.1",
						Weight: 100,
					},
				},
			},
		},
	}

	if err := r.OnConfigChange(newConfig); err != nil {
		t.Fatalf("OnConfigChange returned error: %v", err)
	}

	// Wait for reconciliation
	time.Sleep(200 * time.Millisecond)

	r.Stop()

	// Verify backend was added
	if len(vppSyncer.addedBackends["vip1"]) != 1 {
		t.Errorf("Expected 1 added backend for vip1, got %d", len(vppSyncer.addedBackends["vip1"]))
	}
}

func TestReconcileError(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 100 * time.Millisecond,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	// Set VPP syncer to return error
	vppSyncer.syncErr = errors.New("vpp sync error")

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	// Start reconciler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Set desired config
	newConfig := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
		},
	}

	if err := r.OnConfigChange(newConfig); err != nil {
		t.Fatalf("OnConfigChange returned error: %v", err)
	}

	// Wait for reconciliation attempt
	time.Sleep(200 * time.Millisecond)

	r.Stop()

	// Reconciliation should fail gracefully without panicking
	// No specific assertions needed - test passes if no panic occurs
}

func TestTriggerReconcile(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ReconcileInterval: 1 * time.Hour, // Long interval
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	stateManager := state.NewManager()
	vppSyncer := newMockVPPSyncer()
	healthChecker := newMockHealthCheckManager()

	r := NewReconciler(cfg, logger, stateManager, vppSyncer, healthChecker)

	// Start reconciler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	// Set desired config
	newConfig := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
		},
	}

	stateManager.UpdateConfig(newConfig)

	// Trigger immediate reconciliation
	r.TriggerReconcile()

	// Wait a bit for reconciliation to occur
	time.Sleep(100 * time.Millisecond)

	r.Stop()

	// Verify VIP was synced even though normal interval is very long
	if len(vppSyncer.syncedVIPs) != 1 {
		t.Errorf("Expected 1 synced VIP after manual trigger, got %d", len(vppSyncer.syncedVIPs))
	}
}
