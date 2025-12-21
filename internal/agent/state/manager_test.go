package state

import (
	"testing"

	"github.com/akam1o/arca-lb/internal/common/models"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}

	if m.GetConfig() != nil {
		t.Error("Expected nil config for new manager")
	}

	if m.GetRevision() != 0 {
		t.Error("Expected revision 0 for new manager")
	}
}

func TestUpdateConfig(t *testing.T) {
	m := NewManager()

	config := &models.Config{
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

	m.UpdateConfig(config)

	retrievedConfig := m.GetConfig()
	if retrievedConfig == nil {
		t.Fatal("GetConfig returned nil")
	}

	if retrievedConfig.Revision != 1 {
		t.Errorf("Expected revision 1, got %d", retrievedConfig.Revision)
	}

	if len(retrievedConfig.VIPs) != 1 {
		t.Errorf("Expected 1 VIP, got %d", len(retrievedConfig.VIPs))
	}
}

func TestGetVIP(t *testing.T) {
	m := NewManager()

	config := &models.Config{
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

	m.UpdateConfig(config)

	vip := m.GetVIP("vip1")
	if vip == nil {
		t.Fatal("GetVIP returned nil")
	}

	if vip.VIP.ID != "vip1" {
		t.Errorf("Expected VIP ID 'vip1', got '%s'", vip.VIP.ID)
	}

	// Test non-existent VIP
	vip = m.GetVIP("nonexistent")
	if vip != nil {
		t.Error("Expected nil for non-existent VIP")
	}
}

func TestGetBackendsByVIP(t *testing.T) {
	m := NewManager()

	config := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID: "vip1",
				},
				Backends: []models.Backend{
					{
						ID:     "backend1",
						VIPID:  "vip1",
						IP:     "192.168.1.1",
						Weight: 100,
					},
					{
						ID:     "backend2",
						VIPID:  "vip1",
						IP:     "192.168.1.2",
						Weight: 50,
					},
				},
			},
		},
	}

	m.UpdateConfig(config)

	backends := m.GetBackendsByVIP("vip1")
	if len(backends) != 2 {
		t.Errorf("Expected 2 backends, got %d", len(backends))
	}

	// Test non-existent VIP
	backends = m.GetBackendsByVIP("nonexistent")
	if backends != nil {
		t.Error("Expected nil for non-existent VIP")
	}
}

func TestComputeDiff(t *testing.T) {
	m := NewManager()

	// Initial config
	oldConfig := &models.Config{
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

	m.UpdateConfig(oldConfig)

	// New config with changes
	newConfig := &models.Config{
		Revision: 2,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID:       "vip1",
					VIP:      "10.0.0.1",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodRoundRobin, // Changed
				},
				Backends: []models.Backend{
					{
						ID:     "backend1",
						VIPID:  "vip1",
						IP:     "192.168.1.1",
						Weight: 50, // Changed
					},
					{
						ID:     "backend2", // Added
						VIPID:  "vip1",
						IP:     "192.168.1.2",
						Weight: 100,
					},
				},
			},
			{
				VIP: models.VIP{
					ID:       "vip2", // Added
					VIP:      "10.0.0.2",
					Port:     443,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				},
			},
		},
	}

	diff := m.ComputeDiff(newConfig)

	// Check added VIPs
	if len(diff.AddedVIPs) != 1 {
		t.Errorf("Expected 1 added VIP, got %d", len(diff.AddedVIPs))
	}

	// Check modified VIPs
	if len(diff.ModifiedVIPs) != 1 {
		t.Errorf("Expected 1 modified VIP, got %d", len(diff.ModifiedVIPs))
	}

	// Check added backends (backend2 added + backend1 modified = 2)
	if len(diff.AddedBackends) != 2 {
		t.Errorf("Expected 2 added backends, got %d", len(diff.AddedBackends))
	}

	// Check removed backends (backend1 was modified, so old version is removed)
	if len(diff.RemovedBackends) != 1 {
		t.Errorf("Expected 1 removed backend, got %d", len(diff.RemovedBackends))
	}

	// Test IsEmpty
	if diff.IsEmpty() {
		t.Error("Expected non-empty diff")
	}

	// Test Summary
	summary := diff.Summary()
	if summary["added_vips"] != 1 {
		t.Errorf("Expected 1 added VIP in summary, got %d", summary["added_vips"])
	}
}

func TestDeepCopy(t *testing.T) {
	m := NewManager()

	config := &models.Config{
		Revision: 1,
		VIPs: []models.VIPConfig{
			{
				VIP: models.VIP{
					ID: "vip1",
				},
				HealthCheck: &models.HealthCheck{
					ID:          "hc1",
					VIPID:       "vip1",
					Type:        models.HCTypeHTTP,
					IntervalSec: 5,
					Config: models.HCConfig{
						"path": "/health",
						"nested": map[string]interface{}{
							"key": "value",
						},
					},
				},
				Backends: []models.Backend{
					{
						ID:    "backend1",
						VIPID: "vip1",
						IP:    "192.168.1.1",
					},
				},
			},
		},
	}

	m.UpdateConfig(config)

	// Get a copy
	vip := m.GetVIP("vip1")
	if vip == nil {
		t.Fatal("GetVIP returned nil")
	}

	// Modify the copy
	vip.VIP.Port = 8080
	vip.Backends[0].Weight = 999
	if vip.HealthCheck != nil && vip.HealthCheck.Config != nil {
		vip.HealthCheck.Config["path"] = "/modified"
	}

	// Original should be unchanged
	originalVIP := m.GetVIP("vip1")
	if originalVIP.VIP.Port == 8080 {
		t.Error("Deep copy failed: original VIP was modified")
	}
	if originalVIP.Backends[0].Weight == 999 {
		t.Error("Deep copy failed: original backend was modified")
	}
	if originalVIP.HealthCheck != nil && originalVIP.HealthCheck.Config != nil {
		if path, ok := originalVIP.HealthCheck.Config["path"].(string); ok && path == "/modified" {
			t.Error("Deep copy failed: original health check config was modified")
		}
	}
}

func TestHealthCheckEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        *models.HealthCheck
		b        *models.HealthCheck
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name: "one nil",
			a: &models.HealthCheck{
				Type: models.HCTypeHTTP,
			},
			b:        nil,
			expected: false,
		},
		{
			name: "equal simple",
			a: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 5,
				TimeoutSec:  3,
				RiseCount:   2,
				FallCount:   3,
			},
			b: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 5,
				TimeoutSec:  3,
				RiseCount:   2,
				FallCount:   3,
			},
			expected: true,
		},
		{
			name: "different type",
			a: &models.HealthCheck{
				Type: models.HCTypeHTTP,
			},
			b: &models.HealthCheck{
				Type: models.HCTypeTCP,
			},
			expected: false,
		},
		{
			name: "equal with config",
			a: &models.HealthCheck{
				Type: models.HCTypeHTTP,
				Config: models.HCConfig{
					"path": "/health",
					"port": float64(8080),
				},
			},
			b: &models.HealthCheck{
				Type: models.HCTypeHTTP,
				Config: models.HCConfig{
					"path": "/health",
					"port": float64(8080),
				},
			},
			expected: true,
		},
		{
			name: "different config",
			a: &models.HealthCheck{
				Type: models.HCTypeHTTP,
				Config: models.HCConfig{
					"path": "/health",
				},
			},
			b: &models.HealthCheck{
				Type: models.HCTypeHTTP,
				Config: models.HCConfig{
					"path": "/status",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := healthCheckEqual(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestInterfaceEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        interface{}
		b        interface{}
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "string equal",
			a:        "test",
			b:        "test",
			expected: true,
		},
		{
			name:     "string not equal",
			a:        "test",
			b:        "other",
			expected: false,
		},
		{
			name:     "int equal",
			a:        42,
			b:        42,
			expected: true,
		},
		{
			name:     "map equal",
			a:        map[string]interface{}{"key": "value"},
			b:        map[string]interface{}{"key": "value"},
			expected: true,
		},
		{
			name:     "nested map equal",
			a:        map[string]interface{}{"nested": map[string]interface{}{"key": "value"}},
			b:        map[string]interface{}{"nested": map[string]interface{}{"key": "value"}},
			expected: true,
		},
		{
			name:     "slice equal",
			a:        []interface{}{"a", "b", "c"},
			b:        []interface{}{"a", "b", "c"},
			expected: true,
		},
		{
			name:     "slice not equal",
			a:        []interface{}{"a", "b"},
			b:        []interface{}{"a", "c"},
			expected: false,
		},
		{
			name:     "different types",
			a:        "42",
			b:        42,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := interfaceEqual(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
