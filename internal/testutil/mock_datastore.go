package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
)

// MockDataStore is a mock implementation of datastore.DataStore for testing
type MockDataStore struct {
	mu sync.RWMutex

	// VIP storage
	vips map[string]*models.VIP

	// Backend storage (keyed by backend ID)
	backends map[string]*models.Backend

	// Backend index by VIP ID
	vipBackends map[string][]string

	// Revision
	revision uint64

	// Config and revision for testing
	config       *models.Config
	watchChannel chan datastore.WatchEvent

	// Error injection
	createVIPError         error
	getVIPError            error
	listVIPsError          error
	updateVIPError         error
	deleteVIPError         error
	createBackendError     error
	getBackendError        error
	listBackendsError      error
	updateBackendError     error
	deleteBackendError     error
	getRevisionError       error
	incrementRevisionError error
	getConfigError         error
}

// NewMockDataStore creates a new mock datastore
func NewMockDataStore() *MockDataStore {
	return &MockDataStore{
		vips:        make(map[string]*models.VIP),
		backends:    make(map[string]*models.Backend),
		vipBackends: make(map[string][]string),
		revision:    1,
	}
}

// SetCreateVIPError sets an error to return on CreateVIP
func (m *MockDataStore) SetCreateVIPError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createVIPError = err
}

// SetGetVIPError sets an error to return on GetVIP
func (m *MockDataStore) SetGetVIPError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getVIPError = err
}

// SetListVIPsError sets an error to return on ListVIPs
func (m *MockDataStore) SetListVIPsError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listVIPsError = err
}

// SetCreateBackendError sets an error to return on AddBackend
func (m *MockDataStore) SetCreateBackendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createBackendError = err
}

// SetGetBackendError sets an error to return on GetBackend
func (m *MockDataStore) SetGetBackendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBackendError = err
}

// SetListBackendsError sets an error to return on ListBackends
func (m *MockDataStore) SetListBackendsError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listBackendsError = err
}

// SetUpdateVIPError sets an error to return on UpdateVIP
func (m *MockDataStore) SetUpdateVIPError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateVIPError = err
}

// SetDeleteVIPError sets an error to return on DeleteVIP
func (m *MockDataStore) SetDeleteVIPError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteVIPError = err
}

// SetUpdateBackendError sets an error to return on UpdateBackend
func (m *MockDataStore) SetUpdateBackendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBackendError = err
}

// SetDeleteBackendError sets an error to return on DeleteBackend
func (m *MockDataStore) SetDeleteBackendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteBackendError = err
}

// CreateVIP implements datastore.DataStore
func (m *MockDataStore) CreateVIP(ctx context.Context, vip *models.VIP) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createVIPError != nil {
		return m.createVIPError
	}

	// Generate ID if not set
	if vip.ID == "" {
		vip.ID = "vip-" + generateID()
	}

	// Deep copy
	vipCopy := *vip
	m.vips[vip.ID] = &vipCopy
	m.revision++

	return nil
}

// GetVIP implements datastore.DataStore
func (m *MockDataStore) GetVIP(ctx context.Context, id string) (*models.VIP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getVIPError != nil {
		return nil, m.getVIPError
	}

	vip, exists := m.vips[id]
	if !exists {
		return nil, datastore.ErrNotFound
	}

	// Return a copy
	vipCopy := *vip
	return &vipCopy, nil
}

// ListVIPs implements datastore.DataStore
func (m *MockDataStore) ListVIPs(ctx context.Context) ([]models.VIP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.listVIPsError != nil {
		return nil, m.listVIPsError
	}

	vips := make([]models.VIP, 0, len(m.vips))
	for _, vip := range m.vips {
		vipCopy := *vip
		vips = append(vips, vipCopy)
	}

	return vips, nil
}

// UpdateVIP implements datastore.DataStore
func (m *MockDataStore) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateVIPError != nil {
		return m.updateVIPError
	}

	if _, exists := m.vips[vip.ID]; !exists {
		return datastore.ErrNotFound
	}

	vipCopy := *vip
	m.vips[vip.ID] = &vipCopy
	m.revision++

	return nil
}

// DeleteVIP implements datastore.DataStore
func (m *MockDataStore) DeleteVIP(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteVIPError != nil {
		return m.deleteVIPError
	}

	if _, exists := m.vips[id]; !exists {
		return datastore.ErrNotFound
	}

	// Delete associated backends
	if backendIDs, ok := m.vipBackends[id]; ok {
		for _, backendID := range backendIDs {
			delete(m.backends, backendID)
		}
		delete(m.vipBackends, id)
	}

	delete(m.vips, id)
	m.revision++

	return nil
}

// AddBackend implements datastore.DataStore
func (m *MockDataStore) AddBackend(ctx context.Context, backend *models.Backend) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createBackendError != nil {
		return m.createBackendError
	}

	// Verify VIP exists
	if _, exists := m.vips[backend.VIPID]; !exists {
		return datastore.ErrNotFound
	}

	// Generate ID if not set
	if backend.ID == "" {
		backend.ID = "backend-" + generateID()
	}

	// Deep copy
	backendCopy := *backend
	m.backends[backend.ID] = &backendCopy

	// Update VIP index
	m.vipBackends[backend.VIPID] = append(m.vipBackends[backend.VIPID], backend.ID)
	m.revision++

	return nil
}

// GetBackend implements datastore.DataStore
func (m *MockDataStore) GetBackend(ctx context.Context, id string) (*models.Backend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getBackendError != nil {
		return nil, m.getBackendError
	}

	backend, exists := m.backends[id]
	if !exists {
		return nil, datastore.ErrNotFound
	}

	backendCopy := *backend
	return &backendCopy, nil
}

// ListBackends implements datastore.DataStore
func (m *MockDataStore) ListBackends(ctx context.Context, vipID string) ([]models.Backend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.listBackendsError != nil {
		return nil, m.listBackendsError
	}

	backendIDs, exists := m.vipBackends[vipID]
	if !exists {
		return []models.Backend{}, nil
	}

	backends := make([]models.Backend, 0, len(backendIDs))
	for _, backendID := range backendIDs {
		if backend, exists := m.backends[backendID]; exists {
			backendCopy := *backend
			backends = append(backends, backendCopy)
		}
	}

	return backends, nil
}

// UpdateBackend implements datastore.DataStore
func (m *MockDataStore) UpdateBackend(ctx context.Context, backend *models.Backend) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateBackendError != nil {
		return m.updateBackendError
	}

	if _, exists := m.backends[backend.ID]; !exists {
		return datastore.ErrNotFound
	}

	backendCopy := *backend
	m.backends[backend.ID] = &backendCopy
	m.revision++

	return nil
}

// DeleteBackend implements datastore.DataStore
func (m *MockDataStore) DeleteBackend(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteBackendError != nil {
		return m.deleteBackendError
	}

	backend, exists := m.backends[id]
	if !exists {
		return datastore.ErrNotFound
	}

	// Remove from VIP index
	if backendIDs, ok := m.vipBackends[backend.VIPID]; ok {
		newBackendIDs := make([]string, 0, len(backendIDs))
		for _, bid := range backendIDs {
			if bid != id {
				newBackendIDs = append(newBackendIDs, bid)
			}
		}
		m.vipBackends[backend.VIPID] = newBackendIDs
	}

	delete(m.backends, id)
	m.revision++

	return nil
}

// GetRevision implements datastore.DataStore
func (m *MockDataStore) GetRevision(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getRevisionError != nil {
		return 0, m.getRevisionError
	}

	return int64(m.revision), nil
}

// IncrementRevision implements datastore.DataStore
func (m *MockDataStore) IncrementRevision(ctx context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.incrementRevisionError != nil {
		return 0, m.incrementRevisionError
	}

	m.revision++
	return int64(m.revision), nil
}

// GetConfig implements datastore.DataStore
func (m *MockDataStore) GetConfig(ctx context.Context) (*models.Config, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getConfigError != nil {
		return nil, m.getConfigError
	}

	// If config is explicitly set, return it
	if m.config != nil {
		configCopy := *m.config
		return &configCopy, nil
	}

	// Otherwise, build config from stored VIPs and backends
	vipConfigs := make([]models.VIPConfig, 0, len(m.vips))
	for _, vip := range m.vips {
		// Get backends for this VIP
		backendIDs := m.vipBackends[vip.ID]
		backends := make([]models.Backend, 0, len(backendIDs))
		for _, backendID := range backendIDs {
			if backend, exists := m.backends[backendID]; exists {
				backendCopy := *backend
				backends = append(backends, backendCopy)
			}
		}

		vipConfig := models.VIPConfig{
			VIP:      *vip,
			Backends: backends,
		}
		// Copy health check if present
		if vip.HealthCheck != nil {
			hcCopy := *vip.HealthCheck
			vipConfig.HealthCheck = &hcCopy
		}

		vipConfigs = append(vipConfigs, vipConfig)
	}

	return &models.Config{
		Revision: int64(m.revision),
		VIPs:     vipConfigs,
	}, nil
}

// Watch implements datastore.DataStore
func (m *MockDataStore) Watch(ctx context.Context) (<-chan datastore.WatchEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return the watch channel if set, otherwise return a closed channel
	if m.watchChannel != nil {
		return m.watchChannel, nil
	}

	ch := make(chan datastore.WatchEvent)
	close(ch)
	return ch, nil
}

// SetConfig sets the config to return from GetConfig
func (m *MockDataStore) SetConfig(config *models.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
}

// SetRevision sets the revision to return from GetRevision
func (m *MockDataStore) SetRevision(rev int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision = uint64(rev)
}

// SetWatchChannel sets the watch channel to return from Watch
func (m *MockDataStore) SetWatchChannel(ch chan datastore.WatchEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watchChannel = ch
}

// SetGetConfigError sets an error to return on GetConfig
func (m *MockDataStore) SetGetConfigError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getConfigError = err
}

// SetGetRevisionError sets an error to return on GetRevision
func (m *MockDataStore) SetGetRevisionError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getRevisionError = err
}

// BeginTx implements datastore.DataStore
func (m *MockDataStore) BeginTx(ctx context.Context) (datastore.Transaction, error) {
	// Mock implementation - return a no-op transaction
	return &mockTransaction{store: m}, nil
}

// mockTransaction is a mock transaction
type mockTransaction struct {
	store *MockDataStore
}

func (t *mockTransaction) CreateVIP(ctx context.Context, vip *models.VIP) error {
	return t.store.CreateVIP(ctx, vip)
}

func (t *mockTransaction) AddBackend(ctx context.Context, backend *models.Backend) error {
	return t.store.AddBackend(ctx, backend)
}

func (t *mockTransaction) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	return t.store.UpdateVIP(ctx, vip)
}

func (t *mockTransaction) DeleteVIP(ctx context.Context, id string) error {
	return t.store.DeleteVIP(ctx, id)
}

func (t *mockTransaction) Commit() error {
	return nil
}

func (t *mockTransaction) Rollback() error {
	return nil
}

// Close implements datastore.DataStore
func (m *MockDataStore) Close() error {
	return nil
}

var (
	idCounter int64
	idMutex   sync.Mutex
)

// generateID generates a simple ID for testing
func generateID() string {
	idMutex.Lock()
	defer idMutex.Unlock()
	idCounter++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), idCounter)
}

// GenerateUUID generates a UUID for testing
func GenerateUUID() string {
	return uuid.New().String()
}
