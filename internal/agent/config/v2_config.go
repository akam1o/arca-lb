// Package config provides configuration for the v2 agent.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/common/models"
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
	Type      string             `yaml:"type"` // "vpp" or "noop"
	VPP       *VPPSettings       `yaml:"vpp,omitempty"`
	VPPConfig VPPDataPlaneConfig `yaml:"-"`
}

// VPPSettings contains YAML-facing VPP settings before defaults are applied.
// Pointer fields distinguish omitted settings from explicit zero values.
type VPPSettings struct {
	SocketPath                   *string        `yaml:"socket_path,omitempty"`
	ConnectTimeout               *time.Duration `yaml:"connect_timeout,omitempty"`
	ReconnectInterval            *time.Duration `yaml:"reconnect_interval,omitempty"`
	EncapType                    *string        `yaml:"encap_type,omitempty"`
	DSCP                         *int64         `yaml:"dscp,omitempty"`
	ServiceType                  *string        `yaml:"service_type,omitempty"`
	NewFlowsTableLength          *int64         `yaml:"new_flows_table_length,omitempty"`
	FailOnAllBackendsDown        *bool          `yaml:"fail_on_all_backends_down,omitempty"`
	StateVerificationInterval    *time.Duration `yaml:"state_verification_interval,omitempty"`
	RetainedVIPTuningDriftPolicy *string        `yaml:"retained_vip_tuning_drift_policy,omitempty"`
	RetainedVIPTuningDriftDrain  *time.Duration `yaml:"retained_vip_tuning_drift_drain,omitempty"`
	RollingRecreateDrain         *time.Duration `yaml:"rolling_recreate_drain,omitempty"`
}

// VPPDataPlaneConfig contains validated VPP dataplane settings.
type VPPDataPlaneConfig struct {
	SocketPath                   string
	ConnectTimeout               time.Duration
	ReconnectInterval            time.Duration
	EncapType                    string
	DSCP                         uint8
	ServiceType                  string
	NewFlowsTableLength          uint32
	FailOnAllBackendsDown        bool
	StateVerificationInterval    time.Duration
	RetainedVIPTuningDriftPolicy string
	RetainedVIPTuningDriftDrain  time.Duration
	RollingRecreateDrain         time.Duration
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
	// WorkerCount is the number of worker goroutines allowed to process queued probe jobs.
	WorkerCount int `yaml:"workerCount"`
	// MaxConcurrentChecks limits active probe executions and sizes the internal job/result queues.
	MaxConcurrentChecks int `yaml:"maxConcurrentChecks"`
	// DefaultTimeout is the fallback timeout for health checks that do not set one.
	DefaultTimeout time.Duration `yaml:"defaultTimeout"`
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
	OTLPInsecure bool   `yaml:"otlpInsecure"`
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
	if err := decodeStrictYAML(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := applyV2EnvOverrides(cfg); err != nil {
		return nil, fmt.Errorf("invalid environment override: %w", err)
	}
	applyV2Defaults(cfg)

	if err := validateV2(cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func applyV2EnvOverrides(cfg *V2Config) error {
	if v := os.Getenv("ARCA_AGENT_ID"); v != "" {
		cfg.Agent.ID = v
	}
	if v := os.Getenv("ARCA_AGENT_STATUS_TTL"); v != "" {
		ttl, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("ARCA_AGENT_STATUS_TTL must be a duration: %w", err)
		}
		cfg.Agent.StatusTTL = ttl
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
	if v := os.Getenv("ARCA_OTLP_INSECURE"); v != "" {
		insecure, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("ARCA_OTLP_INSECURE must be a boolean: %w", err)
		}
		cfg.Telemetry.OTLPInsecure = insecure
	}
	if v := os.Getenv("ARCA_METRICS_ADDRESS"); v != "" {
		cfg.Metrics.Address = v
	}
	if v := os.Getenv("ARCA_ROLLOUT_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("ARCA_ROLLOUT_ENABLED must be a boolean: %w", err)
		}
		cfg.Rollout.Enabled = enabled
	}
	if v := os.Getenv("ARCA_ROLLOUT_LEASE_NAMESPACE"); v != "" {
		cfg.Rollout.LeaseNamespace = v
	}
	return nil
}

func applyV2Defaults(cfg *V2Config) {
	if cfg.Agent.StorePath == "" {
		cfg.Agent.StorePath = "/var/lib/arca-lb/agent.db"
	}
	if cfg.Agent.ReconcileInterval == 0 {
		cfg.Agent.ReconcileInterval = 30 * time.Second
	}
	if cfg.Agent.StatusTTL == 0 {
		cfg.Agent.StatusTTL = agentstatus.DefaultAgentStatusTTL
	}
	if cfg.Kubernetes.ResyncInterval == 0 {
		cfg.Kubernetes.ResyncInterval = 30 * time.Second
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
		cfg.Metrics.Address = "127.0.0.1:9090"
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
	if err := models.ValidateAgentID("agent.id", cfg.Agent.ID); err != nil {
		return err
	}
	if cfg.Agent.ReconcileInterval <= 0 {
		return fmt.Errorf("agent.reconcileInterval must be positive")
	}
	if cfg.Agent.StatusTTL <= 0 {
		return fmt.Errorf("agent.statusTTL must be positive")
	}
	if cfg.Kubernetes.ResyncInterval <= 0 {
		return fmt.Errorf("kubernetes.resyncInterval must be positive")
	}
	if cfg.HealthCheck.WorkerCount <= 0 {
		return fmt.Errorf("healthCheck.workerCount must be positive")
	}
	if cfg.HealthCheck.MaxConcurrentChecks <= 0 {
		return fmt.Errorf("healthCheck.maxConcurrentChecks must be positive")
	}
	if cfg.HealthCheck.DefaultTimeout <= 0 {
		return fmt.Errorf("healthCheck.defaultTimeout must be positive")
	}

	if cfg.DataPlane.Type == "" {
		return fmt.Errorf("dataplane.type is required")
	}
	switch cfg.DataPlane.Type {
	case "vpp", "noop":
	default:
		return fmt.Errorf("unsupported dataplane.type: %s", cfg.DataPlane.Type)
	}
	if cfg.DataPlane.Type == "vpp" {
		vppConfig, err := parseV2VPPSettings(cfg.DataPlane.VPP)
		if err != nil {
			return err
		}
		cfg.DataPlane.VPPConfig = vppConfig
	} else {
		cfg.DataPlane.VPPConfig = defaultV2VPPSettings()
	}

	if cfg.Routing.Type == "" {
		return fmt.Errorf("routing.type is required")
	}
	switch cfg.Routing.Type {
	case "frr", "noop":
	default:
		return fmt.Errorf("unsupported routing.type: %s", cfg.Routing.Type)
	}
	if cfg.Routing.CmdTimeout <= 0 {
		return fmt.Errorf("routing.cmdTimeout must be positive")
	}
	if cfg.Rollout.LeaseDuration <= 0 {
		return fmt.Errorf("rollout.leaseDuration must be positive")
	}
	if cfg.Rollout.RetryInterval <= 0 {
		return fmt.Errorf("rollout.retryInterval must be positive")
	}

	if cfg.Metrics.Enabled {
		if !strings.HasPrefix(cfg.Metrics.Path, "/") {
			return fmt.Errorf("metrics.path must be an absolute HTTP path")
		}
		switch cfg.Metrics.Path {
		case "/health", "/livez", "/readyz":
			return fmt.Errorf("metrics.path must not be %s", cfg.Metrics.Path)
		}
	}

	return nil
}

func defaultV2VPPSettings() VPPDataPlaneConfig {
	return VPPDataPlaneConfig{
		SocketPath:                "/run/vpp/api.sock",
		ConnectTimeout:            10 * time.Second,
		ReconnectInterval:         5 * time.Second,
		EncapType:                 "L3DSR",
		DSCP:                      10,
		ServiceType:               "CLUSTERIP",
		NewFlowsTableLength:       65536,
		StateVerificationInterval: 30 * time.Second,
	}
}

func parseV2VPPSettings(vpp *VPPSettings) (VPPDataPlaneConfig, error) {
	cfg := defaultV2VPPSettings()
	if vpp == nil {
		return cfg, nil
	}

	if vpp.SocketPath != nil {
		if *vpp.SocketPath == "" {
			return cfg, fmt.Errorf("dataplane.vpp.socket_path must be a non-empty string")
		}
		cfg.SocketPath = *vpp.SocketPath
	}

	if vpp.EncapType != nil {
		if !validV2VPPEncapType(*vpp.EncapType) {
			return cfg, fmt.Errorf("dataplane.vpp.encap_type must be one of GRE4, GRE6, L3DSR, NAT4, NAT6")
		}
		cfg.EncapType = *vpp.EncapType
	}

	if vpp.DSCP != nil {
		if *vpp.DSCP < 1 || *vpp.DSCP > 63 {
			return cfg, fmt.Errorf("dataplane.vpp.dscp must be an integer between 1 and 63")
		}
		cfg.DSCP = uint8(*vpp.DSCP)
	}

	if vpp.ServiceType != nil {
		if !validV2VPPServiceType(*vpp.ServiceType) {
			return cfg, fmt.Errorf("dataplane.vpp.service_type must be one of CLUSTERIP, NODEPORT")
		}
		cfg.ServiceType = *vpp.ServiceType
	}

	if vpp.NewFlowsTableLength != nil {
		if *vpp.NewFlowsTableLength < 1 || *vpp.NewFlowsTableLength > int64(^uint32(0)) || !isPowerOfTwo(*vpp.NewFlowsTableLength) {
			return cfg, fmt.Errorf("dataplane.vpp.new_flows_table_length must be a power-of-two integer between 1 and %d", uint64(^uint32(0)))
		}
		cfg.NewFlowsTableLength = uint32(*vpp.NewFlowsTableLength)
	}

	if vpp.FailOnAllBackendsDown != nil {
		cfg.FailOnAllBackendsDown = *vpp.FailOnAllBackendsDown
	}

	if vpp.ConnectTimeout != nil {
		if *vpp.ConnectTimeout <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.connect_timeout must be a positive duration")
		}
		cfg.ConnectTimeout = *vpp.ConnectTimeout
	}
	if vpp.ReconnectInterval != nil {
		if *vpp.ReconnectInterval <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.reconnect_interval must be a positive duration")
		}
		cfg.ReconnectInterval = *vpp.ReconnectInterval
	}

	if vpp.RetainedVIPTuningDriftPolicy != nil {
		if *vpp.RetainedVIPTuningDriftPolicy != "" && !validV2VPPTuningDriftPolicy(*vpp.RetainedVIPTuningDriftPolicy) {
			return cfg, fmt.Errorf("dataplane.vpp.retained_vip_tuning_drift_policy must be one of preserve, rolling_recreate")
		}
		cfg.RetainedVIPTuningDriftPolicy = *vpp.RetainedVIPTuningDriftPolicy
	}

	if vpp.StateVerificationInterval != nil {
		if *vpp.StateVerificationInterval <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.state_verification_interval must be a positive duration")
		}
		cfg.StateVerificationInterval = *vpp.StateVerificationInterval
	}

	if vpp.RetainedVIPTuningDriftDrain != nil {
		if *vpp.RetainedVIPTuningDriftDrain <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.retained_vip_tuning_drift_drain must be a positive duration")
		}
		cfg.RetainedVIPTuningDriftDrain = *vpp.RetainedVIPTuningDriftDrain
	}

	if vpp.RollingRecreateDrain != nil {
		if *vpp.RollingRecreateDrain <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.rolling_recreate_drain must be a positive duration")
		}
		cfg.RollingRecreateDrain = *vpp.RollingRecreateDrain
	}

	return cfg, nil
}

func (s *VPPSettings) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("dataplane.vpp must be a mapping")
	}
	*s = VPPSettings{}
	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valueNode := value.Content[i+1]
		key := keyNode.Value
		if !knownV2VPPSetting(key) {
			return fmt.Errorf("dataplane.vpp.%s is not supported", key)
		}

		path := "dataplane.vpp." + key
		switch key {
		case "socket_path":
			field, err := decodeV2StringSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.SocketPath = field
		case "connect_timeout":
			field, err := decodeV2DurationSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.ConnectTimeout = field
		case "reconnect_interval":
			field, err := decodeV2DurationSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.ReconnectInterval = field
		case "encap_type":
			field, err := decodeV2StringSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.EncapType = field
		case "dscp":
			field, err := decodeV2IntegerSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.DSCP = field
		case "service_type":
			field, err := decodeV2StringSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.ServiceType = field
		case "new_flows_table_length":
			field, err := decodeV2IntegerSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.NewFlowsTableLength = field
		case "fail_on_all_backends_down":
			field, err := decodeV2BoolSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.FailOnAllBackendsDown = field
		case "state_verification_interval":
			field, err := decodeV2DurationSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.StateVerificationInterval = field
		case "retained_vip_tuning_drift_policy":
			field, err := decodeV2StringSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.RetainedVIPTuningDriftPolicy = field
		case "retained_vip_tuning_drift_drain":
			field, err := decodeV2DurationSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.RetainedVIPTuningDriftDrain = field
		case "rolling_recreate_drain":
			field, err := decodeV2DurationSetting(path, valueNode)
			if err != nil {
				return err
			}
			s.RollingRecreateDrain = field
		}
	}
	return nil
}

func knownV2VPPSetting(key string) bool {
	switch key {
	case "socket_path",
		"connect_timeout",
		"reconnect_interval",
		"encap_type",
		"dscp",
		"service_type",
		"new_flows_table_length",
		"fail_on_all_backends_down",
		"state_verification_interval",
		"retained_vip_tuning_drift_policy",
		"retained_vip_tuning_drift_drain",
		"rolling_recreate_drain":
		return true
	default:
		return false
	}
}

func validV2VPPEncapType(value string) bool {
	switch value {
	case "GRE4", "GRE6", "L3DSR", "NAT4", "NAT6":
		return true
	default:
		return false
	}
}

func validV2VPPServiceType(value string) bool {
	switch value {
	case "CLUSTERIP", "NODEPORT":
		return true
	default:
		return false
	}
}

func validV2VPPTuningDriftPolicy(value string) bool {
	switch value {
	case "preserve", "rolling_recreate":
		return true
	default:
		return false
	}
}

func isPowerOfTwo(value int64) bool {
	return value > 0 && value&(value-1) == 0
}

func decodeV2StringSetting(path string, value *yaml.Node) (*string, error) {
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return nil, fmt.Errorf("%s must be a string", path)
	}
	parsed := value.Value
	return &parsed, nil
}

func decodeV2BoolSetting(path string, value *yaml.Node) (*bool, error) {
	var parsed bool
	if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" || value.Decode(&parsed) != nil {
		return nil, fmt.Errorf("%s must be a boolean", path)
	}
	return &parsed, nil
}

func decodeV2IntegerSetting(path string, value *yaml.Node) (*int64, error) {
	var parsed int64
	if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || value.Decode(&parsed) != nil {
		return nil, fmt.Errorf("%s must be an integer", path)
	}
	return &parsed, nil
}

func decodeV2DurationSetting(path string, value *yaml.Node) (*time.Duration, error) {
	if value.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("%s must be a duration", path)
	}

	var parsed time.Duration
	switch value.Tag {
	case "!!int":
		var seconds int64
		if err := value.Decode(&seconds); err != nil {
			return nil, fmt.Errorf("%s must be a duration", path)
		}
		parsed = time.Duration(seconds) * time.Second
	case "!!float":
		seconds, err := strconv.ParseFloat(value.Value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a duration", path)
		}
		parsed = time.Duration(seconds * float64(time.Second))
	case "!!str":
		if value.Value == "" {
			return nil, fmt.Errorf("%s must be a duration", path)
		}
		duration, err := time.ParseDuration(value.Value)
		if err != nil {
			return nil, fmt.Errorf("%s must be a duration", path)
		}
		parsed = duration
	default:
		return nil, fmt.Errorf("%s must be a duration", path)
	}
	return &parsed, nil
}
