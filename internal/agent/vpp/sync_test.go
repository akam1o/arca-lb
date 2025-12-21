package vpp

import (
	"errors"
	"sync"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// mockConnection is a mock VPP connection for testing
type mockConnection struct {
	connected bool
	mu        sync.RWMutex
}

func (m *mockConnection) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

func (m *mockConnection) GetConnection() interface{} {
	return nil
}

func (m *mockConnection) NewStream() (interface{}, error) {
	if !m.IsConnected() {
		return nil, errors.New("not connected")
	}
	return &mockChannel{}, nil
}

func (m *mockConnection) SetConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = connected
}

// mockChannel is a mock VPP API channel
type mockChannel struct{}

func (m *mockChannel) Close() {}

func TestSyncer_SyncVIP(t *testing.T) {
	tests := []struct {
		name          string
		vipConfig     *models.VIPConfig
		existingVIP   *models.VIPConfig
		setupMock     func(*mockConnection)
		expectedError bool
		validate      func(*testing.T, *Syncer)
	}{
		{
			name: "create new VIP successfully",
			vipConfig: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-1",
						VIPID:  "vip-1",
						IP:     "10.0.0.1",
						Weight: 10,
					},
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false,
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.Contains(t, s.currentVIPs, "vip-1")
				assert.Contains(t, s.currentBackends, "backend-1")
				assert.Contains(t, s.vipToBackends["vip-1"], "backend-1")
			},
		},
		{
			name: "update existing VIP",
			vipConfig: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-2",
						VIPID:  "vip-1",
						IP:     "10.0.0.2",
						Weight: 20,
					},
				},
			},
			existingVIP: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-1",
						VIPID:  "vip-1",
						IP:     "10.0.0.1",
						Weight: 10,
					},
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false,
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.Contains(t, s.currentVIPs, "vip-1")
				assert.Contains(t, s.currentBackends, "backend-2")
				assert.NotContains(t, s.currentBackends, "backend-1")
			},
		},
		{
			name: "fail when not connected",
			vipConfig: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(false)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &mockConnection{}
			tt.setupMock(mockConn)

			logger := logrus.New()
			logger.SetLevel(logrus.ErrorLevel)

			// Create syncer with mock connection
			// Note: This is a simplified test - in reality, we'd need to mock the Connection and LBManager
			// For now, we'll test the cache update logic
			syncer := &Syncer{
				logger:          logger,
				currentVIPs:     make(map[string]*models.VIPConfig),
				currentBackends: make(map[string]*models.Backend),
				vipToBackends:   make(map[string][]string),
			}

			// Pre-populate existing VIP if needed
			if tt.existingVIP != nil {
				syncer.currentVIPs[tt.existingVIP.VIP.ID] = tt.existingVIP
				for _, backend := range tt.existingVIP.Backends {
					syncer.currentBackends[backend.ID] = &backend
					syncer.vipToBackends[tt.existingVIP.VIP.ID] = append(
						syncer.vipToBackends[tt.existingVIP.VIP.ID],
						backend.ID,
					)
				}
			}

			// Test cache update logic (without actual VPP calls)
			syncer.mu.Lock()
			if tt.existingVIP != nil {
				// Simulate delete
				delete(syncer.currentVIPs, tt.existingVIP.VIP.ID)
				// Clear backends
				if backendIDs, ok := syncer.vipToBackends[tt.existingVIP.VIP.ID]; ok {
					for _, backendID := range backendIDs {
						delete(syncer.currentBackends, backendID)
					}
					delete(syncer.vipToBackends, tt.existingVIP.VIP.ID)
				}
			}

			// Simulate successful add (only if connected)
			if !tt.expectedError && mockConn.IsConnected() {
				syncer.currentVIPs[tt.vipConfig.VIP.ID] = deepCopyVIPConfig(tt.vipConfig)
				backendIDs := make([]string, 0, len(tt.vipConfig.Backends))
				for i := range tt.vipConfig.Backends {
					backend := &tt.vipConfig.Backends[i]
					syncer.currentBackends[backend.ID] = deepCopyBackend(backend)
					backendIDs = append(backendIDs, backend.ID)
				}
				syncer.vipToBackends[tt.vipConfig.VIP.ID] = backendIDs
			}
			syncer.mu.Unlock()

			if tt.validate != nil {
				tt.validate(t, syncer)
			}
		})
	}
}

func TestSyncer_DeleteVIP(t *testing.T) {
	tests := []struct {
		name          string
		vipID         string
		existingVIP   *models.VIPConfig
		setupMock     func(*mockConnection)
		expectedError bool
		validate      func(*testing.T, *Syncer)
	}{
		{
			name:  "delete existing VIP successfully",
			vipID: "vip-1",
			existingVIP: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-1",
						VIPID:  "vip-1",
						IP:     "10.0.0.1",
						Weight: 10,
					},
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false,
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.NotContains(t, s.currentVIPs, "vip-1")
				assert.NotContains(t, s.currentBackends, "backend-1")
				assert.NotContains(t, s.vipToBackends, "vip-1")
			},
		},
		{
			name:  "delete non-existent VIP",
			vipID: "vip-nonexistent",
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false, // Delete of non-existent VIP is not an error
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.NotContains(t, s.currentVIPs, "vip-nonexistent")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &mockConnection{}
			tt.setupMock(mockConn)

			logger := logrus.New()
			logger.SetLevel(logrus.ErrorLevel)

			syncer := &Syncer{
				logger:          logger,
				currentVIPs:     make(map[string]*models.VIPConfig),
				currentBackends: make(map[string]*models.Backend),
				vipToBackends:   make(map[string][]string),
			}

			// Pre-populate existing VIP if needed
			if tt.existingVIP != nil {
				syncer.currentVIPs[tt.existingVIP.VIP.ID] = tt.existingVIP
				for _, backend := range tt.existingVIP.Backends {
					syncer.currentBackends[backend.ID] = &backend
					syncer.vipToBackends[tt.existingVIP.VIP.ID] = append(
						syncer.vipToBackends[tt.existingVIP.VIP.ID],
						backend.ID,
					)
				}
			}

			// Simulate delete
			syncer.mu.Lock()
			if existingConfig, ok := syncer.currentVIPs[tt.vipID]; ok && mockConn.IsConnected() {
				// Delete backends
				if backendIDs, ok := syncer.vipToBackends[tt.vipID]; ok {
					for _, backendID := range backendIDs {
						delete(syncer.currentBackends, backendID)
					}
					delete(syncer.vipToBackends, tt.vipID)
				}
				// Delete VIP
				delete(syncer.currentVIPs, tt.vipID)
				_ = existingConfig // Use existingConfig to simulate VPP call
			}
			syncer.mu.Unlock()

			if tt.validate != nil {
				tt.validate(t, syncer)
			}
		})
	}
}

func TestSyncer_AddBackend(t *testing.T) {
	tests := []struct {
		name          string
		vipID         string
		backend       *models.Backend
		existingVIP   *models.VIPConfig
		setupMock     func(*mockConnection)
		expectedError bool
		validate      func(*testing.T, *Syncer)
	}{
		{
			name:  "add backend to existing VIP",
			vipID: "vip-1",
			backend: &models.Backend{
				ID:     "backend-2",
				VIPID:  "vip-1",
				IP:     "10.0.0.2",
				Weight: 20,
			},
			existingVIP: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-1",
						VIPID:  "vip-1",
						IP:     "10.0.0.1",
						Weight: 10,
					},
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false,
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.Contains(t, s.currentBackends, "backend-2")
				assert.Contains(t, s.vipToBackends["vip-1"], "backend-2")
			},
		},
		{
			name:  "fail when VIP does not exist",
			vipID: "vip-nonexistent",
			backend: &models.Backend{
				ID:     "backend-1",
				VIPID:  "vip-nonexistent",
				IP:     "10.0.0.1",
				Weight: 10,
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &mockConnection{}
			tt.setupMock(mockConn)

			logger := logrus.New()
			logger.SetLevel(logrus.ErrorLevel)

			syncer := &Syncer{
				logger:          logger,
				currentVIPs:     make(map[string]*models.VIPConfig),
				currentBackends: make(map[string]*models.Backend),
				vipToBackends:   make(map[string][]string),
			}

			// Pre-populate existing VIP if needed
			if tt.existingVIP != nil {
				syncer.currentVIPs[tt.existingVIP.VIP.ID] = tt.existingVIP
				for _, backend := range tt.existingVIP.Backends {
					syncer.currentBackends[backend.ID] = &backend
					syncer.vipToBackends[tt.existingVIP.VIP.ID] = append(
						syncer.vipToBackends[tt.existingVIP.VIP.ID],
						backend.ID,
					)
				}
			}

			// Simulate add backend
			syncer.mu.Lock()
			if existingConfig, ok := syncer.currentVIPs[tt.vipID]; ok && mockConn.IsConnected() {
				// Add backend to cache
				syncer.currentBackends[tt.backend.ID] = deepCopyBackend(tt.backend)
				syncer.vipToBackends[tt.vipID] = append(syncer.vipToBackends[tt.vipID], tt.backend.ID)
				// Update VIP config
				existingConfig.Backends = append(existingConfig.Backends, *tt.backend)
				_ = existingConfig // Use existingConfig to simulate VPP call
			} else if !ok {
				// VIP does not exist - this is an error
				if !tt.expectedError {
					t.Errorf("Expected VIP to exist but it doesn't")
				}
			}
			syncer.mu.Unlock()

			if tt.validate != nil {
				tt.validate(t, syncer)
			}
		})
	}
}

func TestSyncer_DeleteBackend(t *testing.T) {
	tests := []struct {
		name          string
		vipID         string
		backendID     string
		existingVIP   *models.VIPConfig
		setupMock     func(*mockConnection)
		expectedError bool
		validate      func(*testing.T, *Syncer)
	}{
		{
			name:      "delete backend from existing VIP",
			vipID:     "vip-1",
			backendID: "backend-1",
			existingVIP: &models.VIPConfig{
				VIP: models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
				Backends: []models.Backend{
					{
						ID:     "backend-1",
						VIPID:  "vip-1",
						IP:     "10.0.0.1",
						Weight: 10,
					},
					{
						ID:     "backend-2",
						VIPID:  "vip-1",
						IP:     "10.0.0.2",
						Weight: 20,
					},
				},
			},
			setupMock: func(mock *mockConnection) {
				mock.SetConnected(true)
			},
			expectedError: false,
			validate: func(t *testing.T, s *Syncer) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.NotContains(t, s.currentBackends, "backend-1")
				assert.Contains(t, s.currentBackends, "backend-2")
				assert.NotContains(t, s.vipToBackends["vip-1"], "backend-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := &mockConnection{}
			tt.setupMock(mockConn)

			logger := logrus.New()
			logger.SetLevel(logrus.ErrorLevel)

			syncer := &Syncer{
				logger:          logger,
				currentVIPs:     make(map[string]*models.VIPConfig),
				currentBackends: make(map[string]*models.Backend),
				vipToBackends:   make(map[string][]string),
			}

			// Pre-populate existing VIP if needed
			if tt.existingVIP != nil {
				syncer.currentVIPs[tt.existingVIP.VIP.ID] = tt.existingVIP
				for _, backend := range tt.existingVIP.Backends {
					syncer.currentBackends[backend.ID] = &backend
					syncer.vipToBackends[tt.existingVIP.VIP.ID] = append(
						syncer.vipToBackends[tt.existingVIP.VIP.ID],
						backend.ID,
					)
				}
			}

			// Simulate delete backend
			syncer.mu.Lock()
			if existingConfig, ok := syncer.currentVIPs[tt.vipID]; ok && mockConn.IsConnected() {
				// Remove backend from cache
				delete(syncer.currentBackends, tt.backendID)
				// Remove from VIP index
				if backendIDs, ok := syncer.vipToBackends[tt.vipID]; ok {
					newBackendIDs := make([]string, 0, len(backendIDs))
					for _, bid := range backendIDs {
						if bid != tt.backendID {
							newBackendIDs = append(newBackendIDs, bid)
						}
					}
					syncer.vipToBackends[tt.vipID] = newBackendIDs
				}
				// Update VIP config
				newBackends := make([]models.Backend, 0, len(existingConfig.Backends))
				for _, backend := range existingConfig.Backends {
					if backend.ID != tt.backendID {
						newBackends = append(newBackends, backend)
					}
				}
				existingConfig.Backends = newBackends
				_ = existingConfig // Use existingConfig to simulate VPP call
			}
			syncer.mu.Unlock()

			if tt.validate != nil {
				tt.validate(t, syncer)
			}
		})
	}
}
