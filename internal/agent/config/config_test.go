package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")

	configContent := `
agent:
  id: "test-agent"
  reconcile_interval: 15s
  heartbeat_interval: 5s

controller:
  address: "localhost:50051"
  timeout: 5s

vpp:
  socket_path: "/tmp/vpp.sock"

log:
  level: "debug"
  format: "text"
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load configuration
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify values
	if cfg.Agent.ID != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", cfg.Agent.ID)
	}

	if cfg.Agent.ReconcileInterval != 15*time.Second {
		t.Errorf("Expected reconcile interval 15s, got %v", cfg.Agent.ReconcileInterval)
	}

	if cfg.Controller.Address != "localhost:50051" {
		t.Errorf("Expected controller address 'localhost:50051', got '%s'", cfg.Controller.Address)
	}
	if cfg.VPP.SocketPath != "/tmp/vpp.sock" {
		t.Errorf("Expected VPP socket '/tmp/vpp.sock', got '%s'", cfg.VPP.SocketPath)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Expected log level 'debug', got '%s'", cfg.Log.Level)
	}

	// Verify defaults were applied
	if cfg.Controller.MaxRetries != 5 {
		t.Errorf("Expected default max retries 5, got %d", cfg.Controller.MaxRetries)
	}

	if cfg.HealthCheck.WorkerCount != 4 {
		t.Errorf("Expected default worker count 4, got %d", cfg.HealthCheck.WorkerCount)
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// Check agent defaults
	if cfg.Agent.ID == "" {
		t.Error("Expected default agent ID to be set")
	}
	if cfg.Agent.ReconcileInterval != 30*time.Second {
		t.Errorf("Expected default reconcile interval 30s, got %v", cfg.Agent.ReconcileInterval)
	}

	// Check controller defaults
	if cfg.Controller.Address != "localhost:50051" {
		t.Errorf("Expected default controller address, got '%s'", cfg.Controller.Address)
	}
	if cfg.Controller.MaxRetries != 5 {
		t.Errorf("Expected default max retries 5, got %d", cfg.Controller.MaxRetries)
	}

	// Check VPP defaults
	if cfg.VPP.SocketPath != "/run/vpp/api.sock" {
		t.Errorf("Expected default VPP socket path, got '%s'", cfg.VPP.SocketPath)
	}

	// Check log defaults
	if cfg.Log.Level != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Expected default log format 'json', got '%s'", cfg.Log.Format)
	}
}

func TestEnvOverrides(t *testing.T) {
	// Set environment variables
	t.Setenv("ARCA_AGENT_ID", "env-agent")
	t.Setenv("ARCA_CONTROLLER_ADDRESS", "env-controller:9999")
	t.Setenv("ARCA_CONTROLLER_API_KEY", "env-controller-secret")
	t.Setenv("ARCA_VPP_SOCKET", "/env/vpp.sock")
	t.Setenv("ARCA_LOG_LEVEL", "error")
	t.Setenv("ARCA_LOG_FORMAT", "text")

	cfg := &Config{
		Agent: AgentConfig{
			ID: "original-agent",
		},
		Controller: ControllerConfig{
			Address: "original:50051",
			APIKey:  "original-controller-secret",
		},
		VPP: VPPConfig{
			SocketPath: "/original/vpp.sock",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}

	applyEnvOverrides(cfg)

	// Verify environment variables override config
	if cfg.Agent.ID != "env-agent" {
		t.Errorf("Expected env override agent ID, got '%s'", cfg.Agent.ID)
	}
	if cfg.Controller.Address != "env-controller:9999" {
		t.Errorf("Expected env override controller address, got '%s'", cfg.Controller.Address)
	}
	if cfg.Controller.APIKey != "env-controller-secret" {
		t.Errorf("Expected env override controller API key, got '%s'", cfg.Controller.APIKey)
	}
	if cfg.VPP.SocketPath != "/env/vpp.sock" {
		t.Errorf("Expected env override VPP socket, got '%s'", cfg.VPP.SocketPath)
	}
	if cfg.Log.Level != "error" {
		t.Errorf("Expected env override log level, got '%s'", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Expected env override log format, got '%s'", cfg.Log.Format)
	}
}

func TestLoadConfigRejectsInvalidControllerAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr string
	}{
		{
			name:    "short",
			apiKey:  "short",
			wantErr: "controller.api_key",
		},
		{
			name:    "whitespace",
			apiKey:  "agent controller secret",
			wantErr: "controller.api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "agent.yaml")
			configContent := `
agent:
  id: "test-agent"

controller:
  address: "localhost:50051"
  api_key: "` + tt.apiKey + `"

vpp:
  socket_path: "/tmp/vpp.sock"

log:
  level: "info"
  format: "json"
`
			if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			_, err := LoadConfig(configPath)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigRejectsControllerAPIKeyWithoutTLS(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	configContent := `
agent:
  id: "test-agent"

controller:
  address: "localhost:50051"
  api_key: "agent-controller-secret"

vpp:
  socket_path: "/tmp/vpp.sock"

log:
  level: "info"
  format: "json"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil || !strings.Contains(err.Error(), "controller.tls.enabled must be enabled when controller.api_key is set") {
		t.Fatalf("LoadConfig error = %v, want controller.api_key TLS validation error", err)
	}
}

func TestLoadConfigAcceptsControllerAPIKeyWithTLS(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	configContent := `
agent:
  id: "test-agent"

controller:
  address: "localhost:50051"
  api_key: "agent-controller-secret"
  tls:
    enabled: true
    ca_file: /tmp/ca.crt

vpp:
  socket_path: "/tmp/vpp.sock"

log:
  level: "info"
  format: "json"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Controller.APIKey != "agent-controller-secret" {
		t.Fatalf("Controller.APIKey = %q", cfg.Controller.APIKey)
	}
	if !cfg.Controller.TLS.Enabled {
		t.Fatal("Controller.TLS.Enabled = false, want true")
	}
	if cfg.Controller.TLS.CertFile != "" || cfg.Controller.TLS.KeyFile != "" {
		t.Fatalf("client certificate files = %q/%q, want optional client cert unset", cfg.Controller.TLS.CertFile, cfg.Controller.TLS.KeyFile)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address:         "localhost:50051",
					Timeout:         10 * time.Second,
					MaxRetries:      5,
					RetryBackoff:    1 * time.Second,
					MaxRetryBackoff: 30 * time.Second,
				},
				VPP: VPPConfig{
					SocketPath:        "/run/vpp/api.sock",
					ConnectTimeout:    5 * time.Second,
					ReconnectInterval: 5 * time.Second,
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount:         4,
					DefaultTimeout:      3 * time.Second,
					MaxConcurrentChecks: 100,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: false,
		},
		{
			name: "missing agent ID",
			cfg: &Config{
				Agent: AgentConfig{
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address: "localhost:50051",
				},
				VPP: VPPConfig{
					SocketPath: "/run/vpp/api.sock",
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount: 4,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "blank agent ID",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "  ",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address: "localhost:50051",
				},
				VPP: VPPConfig{
					SocketPath: "/run/vpp/api.sock",
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount: 4,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address: "localhost:50051",
				},
				VPP: VPPConfig{
					SocketPath: "/run/vpp/api.sock",
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount: 4,
				},
				Log: LogConfig{
					Level:  "invalid",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "TLS enabled without CA",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address:         "localhost:50051",
					Timeout:         10 * time.Second,
					MaxRetries:      5,
					RetryBackoff:    1 * time.Second,
					MaxRetryBackoff: 30 * time.Second,
					TLS: TLSConfig{
						Enabled: true,
					},
				},
				VPP: VPPConfig{
					SocketPath:        "/run/vpp/api.sock",
					ConnectTimeout:    5 * time.Second,
					ReconnectInterval: 5 * time.Second,
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount:         4,
					DefaultTimeout:      3 * time.Second,
					MaxConcurrentChecks: 100,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "TLS enabled with partial client certificate",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address:         "localhost:50051",
					Timeout:         10 * time.Second,
					MaxRetries:      5,
					RetryBackoff:    1 * time.Second,
					MaxRetryBackoff: 30 * time.Second,
					TLS: TLSConfig{
						Enabled:  true,
						CertFile: "/tmp/client.crt",
						CAFile:   "/tmp/ca.crt",
					},
				},
				VPP: VPPConfig{
					SocketPath:        "/run/vpp/api.sock",
					ConnectTimeout:    5 * time.Second,
					ReconnectInterval: 5 * time.Second,
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount:         4,
					DefaultTimeout:      3 * time.Second,
					MaxConcurrentChecks: 100,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "TLS enabled without client certificate",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address:         "localhost:50051",
					Timeout:         10 * time.Second,
					MaxRetries:      5,
					RetryBackoff:    1 * time.Second,
					MaxRetryBackoff: 30 * time.Second,
					TLS: TLSConfig{
						Enabled: true,
						CAFile:  "/tmp/ca.crt",
					},
				},
				VPP: VPPConfig{
					SocketPath:        "/run/vpp/api.sock",
					ConnectTimeout:    5 * time.Second,
					ReconnectInterval: 5 * time.Second,
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount:         4,
					DefaultTimeout:      3 * time.Second,
					MaxConcurrentChecks: 100,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid VPP flow table length",
			cfg: &Config{
				Agent: AgentConfig{
					ID:                "test-agent",
					ReconcileInterval: 30 * time.Second,
					HeartbeatInterval: 10 * time.Second,
				},
				Controller: ControllerConfig{
					Address: "localhost:50051",
				},
				VPP: VPPConfig{
					SocketPath: "/run/vpp/api.sock",
					LB: VPPLBConfig{
						NewFlowsTableLength: 65537,
					},
				},
				HealthCheck: HealthCheckConfig{
					WorkerCount:         4,
					DefaultTimeout:      3 * time.Second,
					MaxConcurrentChecks: 100,
				},
				Log: LogConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
