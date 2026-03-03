package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want json", cfg.Log.Format)
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
