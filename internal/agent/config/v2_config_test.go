package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadV2Config_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")

	// Minimal config
	content := `
agent:
  id: test-agent
dataplane:
  type: noop
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadV2Config(cfgPath)
	if err != nil {
		t.Fatalf("LoadV2Config: %v", err)
	}

	if cfg.Agent.ID != "test-agent" {
		t.Errorf("Agent.ID = %q, want test-agent", cfg.Agent.ID)
	}
	if cfg.DataPlane.Type != "noop" {
		t.Errorf("DataPlane.Type = %q, want noop", cfg.DataPlane.Type)
	}
	// Check defaults
	if cfg.Routing.Type != "noop" {
		t.Errorf("Routing.Type = %q, want noop", cfg.Routing.Type)
	}
	if cfg.HealthCheck.WorkerCount != 4 {
		t.Errorf("HealthCheck.WorkerCount = %d, want 4", cfg.HealthCheck.WorkerCount)
	}
	if cfg.Metrics.Address != ":9090" {
		t.Errorf("Metrics.Address = %q, want :9090", cfg.Metrics.Address)
	}
	if cfg.Rollout.LeaseDuration == 0 {
		t.Error("Rollout.LeaseDuration should default")
	}
	if cfg.Rollout.RetryInterval == 0 {
		t.Error("Rollout.RetryInterval should default")
	}
	if cfg.Agent.StatusTTL != 2*time.Minute {
		t.Errorf("Agent.StatusTTL = %s, want 2m", cfg.Agent.StatusTTL)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want json", cfg.Log.Format)
	}
	if cfg.Telemetry.OTLPInsecure {
		t.Error("Telemetry.OTLPInsecure = true, want false")
	}
}

func TestLoadV2Config_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")

	content := `
agent:
  id: original-id
dataplane:
  type: noop
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ARCA_AGENT_ID", "env-override-id")
	t.Setenv("ARCA_DATAPLANE_TYPE", "vpp")
	t.Setenv("ARCA_ROLLOUT_ENABLED", "true")
	t.Setenv("ARCA_ROLLOUT_LEASE_NAMESPACE", "rollout-ns")
	t.Setenv("ARCA_AGENT_STATUS_TTL", "5m")
	t.Setenv("ARCA_OTLP_ENDPOINT", "collector.example.com:4317")
	t.Setenv("ARCA_OTLP_INSECURE", "true")

	cfg, err := LoadV2Config(cfgPath)
	if err != nil {
		t.Fatalf("LoadV2Config: %v", err)
	}

	if cfg.Agent.ID != "env-override-id" {
		t.Errorf("Agent.ID = %q, want env-override-id", cfg.Agent.ID)
	}
	if cfg.DataPlane.Type != "vpp" {
		t.Errorf("DataPlane.Type = %q, want vpp", cfg.DataPlane.Type)
	}
	if !cfg.Rollout.Enabled {
		t.Error("Rollout.Enabled = false, want true")
	}
	if cfg.Rollout.LeaseNamespace != "rollout-ns" {
		t.Errorf("Rollout.LeaseNamespace = %q, want rollout-ns", cfg.Rollout.LeaseNamespace)
	}
	if cfg.Agent.StatusTTL != 5*time.Minute {
		t.Errorf("Agent.StatusTTL = %s, want 5m", cfg.Agent.StatusTTL)
	}
	if cfg.Telemetry.OTLPEndpoint != "collector.example.com:4317" {
		t.Errorf("Telemetry.OTLPEndpoint = %q, want collector.example.com:4317", cfg.Telemetry.OTLPEndpoint)
	}
	if !cfg.Telemetry.OTLPInsecure {
		t.Error("Telemetry.OTLPInsecure = false, want true")
	}
}

func TestLoadV2Config_TelemetryInsecureYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")

	content := `
agent:
  id: test-agent
dataplane:
  type: noop
telemetry:
  otlpEndpoint: collector.example.com:4317
  otlpInsecure: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadV2Config(cfgPath)
	if err != nil {
		t.Fatalf("LoadV2Config: %v", err)
	}

	if cfg.Telemetry.OTLPEndpoint != "collector.example.com:4317" {
		t.Errorf("Telemetry.OTLPEndpoint = %q, want collector.example.com:4317", cfg.Telemetry.OTLPEndpoint)
	}
	if !cfg.Telemetry.OTLPInsecure {
		t.Error("Telemetry.OTLPInsecure = false, want true")
	}
}

func TestLoadV2Config_InvalidDataPlaneType(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")

	content := `
dataplane:
  type: invalid
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadV2Config(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for invalid dataplane type")
	}
}

func TestLoadV2Config_InvalidHealthCheckSettings(t *testing.T) {
	tests := []struct {
		name       string
		healthYAML string
		wantErr    string
	}{
		{
			name:       "negative worker count",
			healthYAML: "workerCount: -1\n",
			wantErr:    "healthCheck.workerCount",
		},
		{
			name:       "negative max concurrent checks",
			healthYAML: "maxConcurrentChecks: -1\n",
			wantErr:    "healthCheck.maxConcurrentChecks",
		},
		{
			name:       "negative default timeout",
			healthYAML: "defaultTimeout: -1s\n",
			wantErr:    "healthCheck.defaultTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "agent.yaml")
			content := "agent:\n  id: test-agent\ndataplane:\n  type: noop\nhealthCheck:\n"
			for _, line := range strings.Split(strings.TrimSuffix(tt.healthYAML, "\n"), "\n") {
				content += "  " + line + "\n"
			}

			if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadV2Config(cfgPath)
			if err == nil {
				t.Fatalf("expected validation error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadV2Config error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadV2Config_InvalidMetricsPath(t *testing.T) {
	tests := []struct {
		name        string
		metricsYAML string
		wantErr     string
	}{
		{
			name:        "relative path",
			metricsYAML: "enabled: true\npath: metrics\n",
			wantErr:     "metrics.path",
		},
		{
			name:        "health path conflict",
			metricsYAML: "enabled: true\npath: /health\n",
			wantErr:     "metrics.path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "agent.yaml")
			content := "agent:\n  id: test-agent\ndataplane:\n  type: noop\nmetrics:\n"
			for _, line := range strings.Split(strings.TrimSuffix(tt.metricsYAML, "\n"), "\n") {
				content += "  " + line + "\n"
			}

			if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadV2Config(cfgPath)
			if err == nil {
				t.Fatalf("expected validation error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadV2Config error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadV2Config_InvalidVPPSettings(t *testing.T) {
	tests := []struct {
		name    string
		vppYAML string
		wantErr string
	}{
		{
			name:    "invalid encap type",
			vppYAML: "encap_type: L3dsr\n",
			wantErr: "dataplane.vpp.encap_type",
		},
		{
			name:    "invalid service type",
			vppYAML: "service_type: ClusterIP\n",
			wantErr: "dataplane.vpp.service_type",
		},
		{
			name:    "dscp wraps uint8",
			vppYAML: "dscp: 300\n",
			wantErr: "dataplane.vpp.dscp",
		},
		{
			name:    "zero flow table length",
			vppYAML: "new_flows_table_length: 0\n",
			wantErr: "dataplane.vpp.new_flows_table_length",
		},
		{
			name:    "flow table length wraps uint32",
			vppYAML: "new_flows_table_length: 4294967296\n",
			wantErr: "dataplane.vpp.new_flows_table_length",
		},
		{
			name:    "invalid retained tuning drift policy",
			vppYAML: "retained_vip_tuning_drift_policy: rolling-recreate\n",
			wantErr: "dataplane.vpp.retained_vip_tuning_drift_policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "agent.yaml")
			content := "agent:\n  id: test-agent\ndataplane:\n  type: vpp\n  vpp:\n"
			for _, line := range strings.Split(strings.TrimSuffix(tt.vppYAML, "\n"), "\n") {
				content += "    " + line + "\n"
			}

			if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := LoadV2Config(cfgPath)
			if err == nil {
				t.Fatalf("expected validation error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadV2Config error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadV2Config_MissingFile(t *testing.T) {
	_, err := LoadV2Config("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadV2Config_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")

	if err := os.WriteFile(cfgPath, []byte(":::invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadV2Config(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
