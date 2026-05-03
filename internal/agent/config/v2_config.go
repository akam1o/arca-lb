// Package config provides configuration for the v2 agent.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// V2Config is the top-level configuration for the v2 agent.
type V2Config struct {
	Agent       AgentSettings      `yaml:"agent"`
	Kubernetes  KubernetesSettings `yaml:"kubernetes"`
	DataPlane   DataPlaneSettings  `yaml:"dataplane"`
	Routing     RoutingSettings    `yaml:"routing"`
	Rollout     RolloutSettings    `yaml:"rollout"`
	HealthCheck HCSettings         `yaml:"healthCheck"`
	Metrics     MetricsSettings    `yaml:"metrics"`
	Telemetry   TelemetrySettings  `yaml:"telemetry"`
	Log         LogSettings        `yaml:"log"`
}

// AgentSettings contains agent identity and tuning.
type AgentSettings struct {
	ID                string        `yaml:"id"`
	StorePath         string        `yaml:"storePath"`
	ReconcileInterval time.Duration `yaml:"reconcileInterval"`
	StatusTTL         time.Duration `yaml:"statusTTL"`
}

// KubernetesSettings configures K8s API access.
type KubernetesSettings struct {
	Kubeconfig     string        `yaml:"kubeconfig"`
	Namespace      string        `yaml:"namespace"`
	ResyncInterval time.Duration `yaml:"resyncInterval"`
}

// DataPlaneSettings configures the data-plane backend.
type DataPlaneSettings struct {
	Type string                 `yaml:"type"` // "vpp" or "noop"
	VPP  map[string]interface{} `yaml:"vpp,omitempty"`
}

// RoutingSettings configures BGP route management.
type RoutingSettings struct {
	Enabled    bool          `yaml:"enabled"`
	Type       string        `yaml:"type"` // "frr" or "noop"
	VTYShPath  string        `yaml:"vtyshPath"`
	RouteTag   int           `yaml:"routeTag"`
	CmdTimeout time.Duration `yaml:"cmdTimeout"`
}

// RolloutSettings configures cluster-wide serialization of disruptive VIP changes.
type RolloutSettings struct {
	Enabled        bool          `yaml:"enabled"`
	LeaseNamespace string        `yaml:"leaseNamespace"`
	LeaseDuration  time.Duration `yaml:"leaseDuration"`
	RetryInterval  time.Duration `yaml:"retryInterval"`
}

// HCSettings configures the health check engine.
type HCSettings struct {
	WorkerCount         int           `yaml:"workerCount"`
	MaxConcurrentChecks int           `yaml:"maxConcurrentChecks"`
	DefaultTimeout      time.Duration `yaml:"defaultTimeout"`
}

// MetricsSettings configures the Prometheus metrics endpoint.
type MetricsSettings struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
	Path    string `yaml:"path"`
}

// TelemetrySettings configures OpenTelemetry.
type TelemetrySettings struct {
	OTLPEndpoint string `yaml:"otlpEndpoint"`
}

// LogSettings configures logging.
type LogSettings struct {
	Level  string `yaml:"level"`  // "debug", "info", "warn", "error"
	Format string `yaml:"format"` // "json", "text"
}

// LoadV2Config loads agent configuration from a YAML file,
// applying environment variable overrides and defaults.
func LoadV2Config(path string) (*V2Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg := &V2Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	applyV2EnvOverrides(cfg)
	applyV2Defaults(cfg)

	if err := validateV2(cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func applyV2EnvOverrides(cfg *V2Config) {
	if v := os.Getenv("ARCA_AGENT_ID"); v != "" {
		cfg.Agent.ID = v
	}
	if v := os.Getenv("ARCA_AGENT_STATUS_TTL"); v != "" {
		if ttl, err := time.ParseDuration(v); err == nil {
			cfg.Agent.StatusTTL = ttl
		}
	}
	if v := os.Getenv("ARCA_KUBECONFIG"); v != "" {
		cfg.Kubernetes.Kubeconfig = v
	}
	if v := os.Getenv("ARCA_NAMESPACE"); v != "" {
		cfg.Kubernetes.Namespace = v
	}
	if v := os.Getenv("ARCA_DATAPLANE_TYPE"); v != "" {
		cfg.DataPlane.Type = v
	}
	if v := os.Getenv("ARCA_OTLP_ENDPOINT"); v != "" {
		cfg.Telemetry.OTLPEndpoint = v
	}
	if v := os.Getenv("ARCA_METRICS_ADDRESS"); v != "" {
		cfg.Metrics.Address = v
	}
	if v := os.Getenv("ARCA_ROLLOUT_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Rollout.Enabled = enabled
		}
	}
	if v := os.Getenv("ARCA_ROLLOUT_LEASE_NAMESPACE"); v != "" {
		cfg.Rollout.LeaseNamespace = v
	}
}

func applyV2Defaults(cfg *V2Config) {
	if cfg.Agent.StorePath == "" {
		cfg.Agent.StorePath = "/var/lib/arca-lb/agent.db"
	}
	if cfg.Agent.ReconcileInterval == 0 {
		cfg.Agent.ReconcileInterval = 30 * time.Second
	}
	if cfg.Agent.StatusTTL == 0 {
		cfg.Agent.StatusTTL = 4 * cfg.Agent.ReconcileInterval
	}
	if cfg.Kubernetes.ResyncInterval == 0 {
		cfg.Kubernetes.ResyncInterval = 30 * time.Second
	}
	if cfg.DataPlane.Type == "" {
		cfg.DataPlane.Type = "noop"
	}
	if cfg.Routing.Type == "" {
		cfg.Routing.Type = "noop"
	}
	if cfg.Routing.VTYShPath == "" {
		cfg.Routing.VTYShPath = "/usr/bin/vtysh"
	}
	if cfg.Routing.RouteTag == 0 {
		cfg.Routing.RouteTag = 10000
	}
	if cfg.Routing.CmdTimeout == 0 {
		cfg.Routing.CmdTimeout = 10 * time.Second
	}
	if cfg.Rollout.LeaseDuration == 0 {
		cfg.Rollout.LeaseDuration = 2 * time.Minute
	}
	if cfg.Rollout.RetryInterval == 0 {
		cfg.Rollout.RetryInterval = time.Second
	}
	if cfg.HealthCheck.WorkerCount == 0 {
		cfg.HealthCheck.WorkerCount = 4
	}
	if cfg.HealthCheck.MaxConcurrentChecks == 0 {
		cfg.HealthCheck.MaxConcurrentChecks = 64
	}
	if cfg.HealthCheck.DefaultTimeout == 0 {
		cfg.HealthCheck.DefaultTimeout = 3 * time.Second
	}
	if cfg.Metrics.Address == "" {
		cfg.Metrics.Address = ":9090"
	}
	if cfg.Metrics.Path == "" {
		cfg.Metrics.Path = "/metrics"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
}

func validateV2(cfg *V2Config) error {
	if cfg.Agent.ID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("agent.id is required (hostname lookup failed: %w)", err)
		}
		cfg.Agent.ID = hostname
	}
	if cfg.Agent.ReconcileInterval <= 0 {
		return fmt.Errorf("agent.reconcileInterval must be positive")
	}
	if cfg.Agent.StatusTTL <= 0 {
		return fmt.Errorf("agent.statusTTL must be positive")
	}

	switch cfg.DataPlane.Type {
	case "vpp", "noop":
	default:
		return fmt.Errorf("unsupported dataplane.type: %s", cfg.DataPlane.Type)
	}

	switch cfg.Routing.Type {
	case "frr", "noop":
	default:
		return fmt.Errorf("unsupported routing.type: %s", cfg.Routing.Type)
	}

	return nil
}
