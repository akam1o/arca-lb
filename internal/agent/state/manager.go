package state

import (
	"sync"

	"github.com/akam1o/arca-lb/internal/common/models"
)

// Manager manages the current configuration state
type Manager struct {
	mu sync.RWMutex

	// Current configuration
	config *models.Config

	// VIP map for fast lookup
	vips map[string]*models.VIPConfig

	// Backend map for fast lookup (keyed by backend ID)
	backends map[string]*models.Backend

	// VIP to Backends mapping (keyed by VIP ID)
	vipBackends map[string][]string
}

// NewManager creates a new state manager
func NewManager() *Manager {
	return &Manager{
		vips:        make(map[string]*models.VIPConfig),
		backends:    make(map[string]*models.Backend),
		vipBackends: make(map[string][]string),
	}
}

// GetConfig returns the current configuration
func (m *Manager) GetConfig() *models.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config == nil {
		return nil
	}

	// Return a copy to prevent external modifications
	return m.copyConfig(m.config)
}

// UpdateConfig updates the entire configuration
func (m *Manager) UpdateConfig(config *models.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deep copy the config to prevent external modifications
	m.config = m.copyConfig(config)

	// Rebuild lookup maps
	m.rebuildMaps()
}

// GetRevision returns the current configuration revision
func (m *Manager) GetRevision() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config == nil {
		return 0
	}
	return m.config.Revision
}

// GetVIP returns a VIP by ID
func (m *Manager) GetVIP(vipID string) *models.VIPConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vip, exists := m.vips[vipID]
	if !exists {
		return nil
	}

	// Return a deep copy
	return m.deepCopyVIPConfig(vip)
}

// GetVIPs returns all VIPs
func (m *Manager) GetVIPs() []models.VIPConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vips := make([]models.VIPConfig, 0, len(m.vips))
	for _, vip := range m.vips {
		vips = append(vips, *m.deepCopyVIPConfig(vip))
	}
	return vips
}

// GetBackend returns a backend by ID
func (m *Manager) GetBackend(backendID string) *models.Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()

	backend, exists := m.backends[backendID]
	if !exists {
		return nil
	}

	// Return a copy
	backendCopy := *backend
	return &backendCopy
}

// GetBackendsByVIP returns all backends for a VIP
func (m *Manager) GetBackendsByVIP(vipID string) []models.Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()

	backendIDs, exists := m.vipBackends[vipID]
	if !exists {
		return nil
	}

	backends := make([]models.Backend, 0, len(backendIDs))
	for _, backendID := range backendIDs {
		if backend, ok := m.backends[backendID]; ok {
			backends = append(backends, *backend)
		}
	}

	return backends
}

// GetAllBackends returns all backends
func (m *Manager) GetAllBackends() []models.Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()

	backends := make([]models.Backend, 0, len(m.backends))
	for _, backend := range m.backends {
		backends = append(backends, *backend)
	}
	return backends
}

// GetHealthCheck returns the health check configuration for a VIP
func (m *Manager) GetHealthCheck(vipID string) *models.HealthCheck {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vip, exists := m.vips[vipID]
	if !exists || vip.HealthCheck == nil {
		return nil
	}

	// Return a copy
	hcCopy := *vip.HealthCheck
	return &hcCopy
}

// rebuildMaps rebuilds the internal lookup maps
func (m *Manager) rebuildMaps() {
	// Clear existing maps
	m.vips = make(map[string]*models.VIPConfig)
	m.backends = make(map[string]*models.Backend)
	m.vipBackends = make(map[string][]string)

	if m.config == nil {
		return
	}

	// Rebuild maps from config
	for i := range m.config.VIPs {
		vipConfig := &m.config.VIPs[i]
		m.vips[vipConfig.VIP.ID] = vipConfig

		// Build backend maps
		backendIDs := make([]string, 0, len(vipConfig.Backends))
		for j := range vipConfig.Backends {
			backend := &vipConfig.Backends[j]
			m.backends[backend.ID] = backend
			backendIDs = append(backendIDs, backend.ID)
		}
		m.vipBackends[vipConfig.VIP.ID] = backendIDs
	}
}

// copyConfig creates a deep copy of the configuration
func (m *Manager) copyConfig(config *models.Config) *models.Config {
	if config == nil {
		return nil
	}

	copy := &models.Config{
		Revision: config.Revision,
		VIPs:     make([]models.VIPConfig, len(config.VIPs)),
	}

	for i := range config.VIPs {
		copy.VIPs[i] = *m.deepCopyVIPConfig(&config.VIPs[i])
	}

	return copy
}

// deepCopyVIPConfig creates a deep copy of a VIPConfig
func (m *Manager) deepCopyVIPConfig(vipConfig *models.VIPConfig) *models.VIPConfig {
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
		// Deep copy the Config map (including nested values)
		if vipConfig.HealthCheck.Config != nil {
			hcCopy.Config = deepCopyHCConfig(vipConfig.HealthCheck.Config)
		}
		copy.HealthCheck = &hcCopy
	}

	// Copy backends
	for j, backend := range vipConfig.Backends {
		copy.Backends[j] = backend
	}

	return copy
}

// ComputeDiff computes the difference between the current state and a new configuration
func (m *Manager) ComputeDiff(newConfig *models.Config) *ConfigDiff {
	m.mu.RLock()
	defer m.mu.RUnlock()

	diff := &ConfigDiff{
		AddedVIPs:       make([]models.VIPConfig, 0),
		RemovedVIPs:     make([]models.VIPConfig, 0),
		ModifiedVIPs:    make([]models.VIPConfig, 0),
		AddedBackends:   make([]models.Backend, 0),
		RemovedBackends: make([]models.Backend, 0),
	}

	// Handle nil new config - treat as removal of all existing resources
	if newConfig == nil {
		for _, vip := range m.vips {
			diff.RemovedVIPs = append(diff.RemovedVIPs, *vip)
		}
		for _, backend := range m.backends {
			diff.RemovedBackends = append(diff.RemovedBackends, *backend)
		}
		return diff
	}

	// Build maps for new config
	newVIPs := make(map[string]*models.VIPConfig)
	newBackends := make(map[string]*models.Backend)

	for i := range newConfig.VIPs {
		vipConfig := &newConfig.VIPs[i]
		newVIPs[vipConfig.VIP.ID] = vipConfig

		for j := range vipConfig.Backends {
			backend := &vipConfig.Backends[j]
			newBackends[backend.ID] = backend
		}
	}

	// Find added and modified VIPs
	for vipID, newVIP := range newVIPs {
		oldVIP, exists := m.vips[vipID]
		if !exists {
			diff.AddedVIPs = append(diff.AddedVIPs, *newVIP)
		} else if !vipConfigEqual(oldVIP, newVIP) {
			diff.ModifiedVIPs = append(diff.ModifiedVIPs, *newVIP)
		}
	}

	// Find removed VIPs
	for vipID, oldVIP := range m.vips {
		if _, exists := newVIPs[vipID]; !exists {
			diff.RemovedVIPs = append(diff.RemovedVIPs, *oldVIP)
		}
	}

	// Find added and modified backends
	for backendID, newBackend := range newBackends {
		oldBackend, exists := m.backends[backendID]
		if !exists {
			diff.AddedBackends = append(diff.AddedBackends, *newBackend)
		} else if !backendEqual(oldBackend, newBackend) {
			// Backend modified - treat as remove + add
			diff.RemovedBackends = append(diff.RemovedBackends, *oldBackend)
			diff.AddedBackends = append(diff.AddedBackends, *newBackend)
		}
	}

	// Find removed backends
	for backendID, oldBackend := range m.backends {
		if _, exists := newBackends[backendID]; !exists {
			diff.RemovedBackends = append(diff.RemovedBackends, *oldBackend)
		}
	}

	return diff
}

// vipEqual checks if two VIPs are equal
func vipEqual(a, b *models.VIP) bool {
	return a.VIP == b.VIP &&
		a.Port == b.Port &&
		a.Protocol == b.Protocol &&
		a.LBMethod == b.LBMethod
}

// vipConfigEqual checks if two VIPConfigs are equal (including backends and health checks)
func vipConfigEqual(a, b *models.VIPConfig) bool {
	// Check VIP equality
	if !vipEqual(&a.VIP, &b.VIP) {
		return false
	}

	// Check health check equality
	if !healthCheckEqual(a.HealthCheck, b.HealthCheck) {
		return false
	}

	// Check backends count
	if len(a.Backends) != len(b.Backends) {
		return false
	}

	// Build backend map for comparison
	aBackends := make(map[string]models.Backend)
	for _, backend := range a.Backends {
		aBackends[backend.ID] = backend
	}

	for _, bBackend := range b.Backends {
		aBackend, exists := aBackends[bBackend.ID]
		if !exists || !backendEqual(&aBackend, &bBackend) {
			return false
		}
	}

	return true
}

// backendEqual checks if two Backends are equal
func backendEqual(a, b *models.Backend) bool {
	return a.ID == b.ID &&
		a.VIPID == b.VIPID &&
		a.IP == b.IP &&
		a.Weight == b.Weight
}

// healthCheckEqual checks if two HealthChecks are equal
func healthCheckEqual(a, b *models.HealthCheck) bool {
	// Both nil
	if a == nil && b == nil {
		return true
	}
	// One nil
	if a == nil || b == nil {
		return false
	}

	// Compare fields
	if a.Type != b.Type ||
		a.IntervalSec != b.IntervalSec ||
		a.TimeoutSec != b.TimeoutSec ||
		a.RiseCount != b.RiseCount ||
		a.FallCount != b.FallCount {
		return false
	}

	// Compare Config maps safely (can't use != for non-comparable types)
	return hcConfigEqual(a.Config, b.Config)
}

// deepCopyHCConfig creates a deep copy of HCConfig map
func deepCopyHCConfig(config models.HCConfig) models.HCConfig {
	if config == nil {
		return nil
	}

	copy := make(models.HCConfig, len(config))
	for k, v := range config {
		copy[k] = deepCopyInterface(v)
	}
	return copy
}

// deepCopyInterface creates a deep copy of an interface{} value
func deepCopyInterface(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, v := range val {
			m[k] = deepCopyInterface(v)
		}
		return m
	case []interface{}:
		s := make([]interface{}, len(val))
		for i, v := range val {
			s[i] = deepCopyInterface(v)
		}
		return s
	case string, int, int64, float64, bool:
		// Primitive types are safe to copy directly
		return val
	default:
		// For other types, return as-is (this is best effort)
		return val
	}
}

// hcConfigEqual compares two HCConfig maps safely
func hcConfigEqual(a, b models.HCConfig) bool {
	if len(a) != len(b) {
		return false
	}

	for k, av := range a {
		bv, exists := b[k]
		if !exists || !interfaceEqual(av, bv) {
			return false
		}
	}

	return true
}

// interfaceEqual compares two interface{} values safely
func interfaceEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if bvv, exists := bv[k]; !exists || !interfaceEqual(v, bvv) {
				return false
			}
		}
		return true
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for i, v := range av {
			if !interfaceEqual(v, bv[i]) {
				return false
			}
		}
		return true
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case int:
		bv, ok := b.(int)
		return ok && av == bv
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	default:
		// For other types, return false (safer than panic)
		// If we encounter unhandled types, we treat them as not equal
		return false
	}
}

// ConfigDiff represents the difference between two configurations
type ConfigDiff struct {
	AddedVIPs       []models.VIPConfig
	RemovedVIPs     []models.VIPConfig
	ModifiedVIPs    []models.VIPConfig
	AddedBackends   []models.Backend
	RemovedBackends []models.Backend
}

// IsEmpty returns true if there are no changes
func (d *ConfigDiff) IsEmpty() bool {
	return len(d.AddedVIPs) == 0 &&
		len(d.RemovedVIPs) == 0 &&
		len(d.ModifiedVIPs) == 0 &&
		len(d.AddedBackends) == 0 &&
		len(d.RemovedBackends) == 0
}

// Summary returns a summary of the diff
func (d *ConfigDiff) Summary() map[string]int {
	return map[string]int{
		"added_vips":       len(d.AddedVIPs),
		"removed_vips":     len(d.RemovedVIPs),
		"modified_vips":    len(d.ModifiedVIPs),
		"added_backends":   len(d.AddedBackends),
		"removed_backends": len(d.RemovedBackends),
	}
}
