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
	Type      string                 `yaml:"type"` // "vpp" or "noop"
	VPP       map[string]interface{} `yaml:"vpp,omitempty"`
	VPPConfig VPPDataPlaneConfig     `yaml:"-"`
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

func parseV2VPPSettings(vpp map[string]interface{}) (VPPDataPlaneConfig, error) {
	cfg := defaultV2VPPSettings()
	if vpp == nil {
		return cfg, nil
	}

	for key := range vpp {
		if !knownV2VPPSetting(key) {
			return cfg, fmt.Errorf("dataplane.vpp.%s is not supported", key)
		}
	}

	if value, ok := vpp["socket_path"]; ok {
		socketPath, ok := value.(string)
		if !ok || socketPath == "" {
			return cfg, fmt.Errorf("dataplane.vpp.socket_path must be a non-empty string")
		}
		cfg.SocketPath = socketPath
	}

	if value, ok := vpp["encap_type"]; ok {
		encapType, ok := value.(string)
		if !ok || !validV2VPPEncapType(encapType) {
			return cfg, fmt.Errorf("dataplane.vpp.encap_type must be one of GRE4, GRE6, L3DSR, NAT4, NAT6")
		}
		cfg.EncapType = encapType
	}

	if value, ok := vpp["dscp"]; ok {
		dscp, ok := v2IntegerSetting(value)
		if !ok || dscp < 1 || dscp > 63 {
			return cfg, fmt.Errorf("dataplane.vpp.dscp must be an integer between 1 and 63")
		}
		cfg.DSCP = uint8(dscp)
	}

	if value, ok := vpp["service_type"]; ok {
		serviceType, ok := value.(string)
		if !ok || !validV2VPPServiceType(serviceType) {
			return cfg, fmt.Errorf("dataplane.vpp.service_type must be one of CLUSTERIP, NODEPORT")
		}
		cfg.ServiceType = serviceType
	}

	if value, ok := vpp["new_flows_table_length"]; ok {
		tableLength, ok := v2IntegerSetting(value)
		if !ok || tableLength < 1 || tableLength > int64(^uint32(0)) || !isPowerOfTwo(tableLength) {
			return cfg, fmt.Errorf("dataplane.vpp.new_flows_table_length must be a power-of-two integer between 1 and %d", uint64(^uint32(0)))
		}
		cfg.NewFlowsTableLength = uint32(tableLength)
	}

	if value, ok := vpp["fail_on_all_backends_down"]; ok {
		if _, ok := value.(bool); !ok {
			return cfg, fmt.Errorf("dataplane.vpp.fail_on_all_backends_down must be a boolean")
		}
		cfg.FailOnAllBackendsDown = value.(bool)
	}

	for _, key := range []string{"connect_timeout", "reconnect_interval"} {
		if value, ok := vpp[key]; ok {
			timeout, ok := v2DurationSetting(value)
			if !ok || timeout <= 0 {
				return cfg, fmt.Errorf("dataplane.vpp.%s must be a positive duration", key)
			}
			switch key {
			case "connect_timeout":
				cfg.ConnectTimeout = timeout
			case "reconnect_interval":
				cfg.ReconnectInterval = timeout
			}
		}
	}

	if value, ok := vpp["retained_vip_tuning_drift_policy"]; ok {
		policy, ok := value.(string)
		if !ok || (policy != "" && !validV2VPPTuningDriftPolicy(policy)) {
			return cfg, fmt.Errorf("dataplane.vpp.retained_vip_tuning_drift_policy must be one of preserve, rolling_recreate")
		}
		cfg.RetainedVIPTuningDriftPolicy = policy
	}

	if value, ok := vpp["state_verification_interval"]; ok {
		interval, ok := v2DurationSetting(value)
		if !ok || interval <= 0 {
			return cfg, fmt.Errorf("dataplane.vpp.state_verification_interval must be a positive duration")
		}
		cfg.StateVerificationInterval = interval
	}

	for _, key := range []string{"retained_vip_tuning_drift_drain", "rolling_recreate_drain"} {
		if value, ok := vpp[key]; ok {
			drain, ok := v2DurationSetting(value)
			if !ok || drain <= 0 {
				return cfg, fmt.Errorf("dataplane.vpp.%s must be a positive duration", key)
			}
			switch key {
			case "retained_vip_tuning_drift_drain":
				cfg.RetainedVIPTuningDriftDrain = drain
			case "rolling_recreate_drain":
				cfg.RollingRecreateDrain = drain
			}
		}
	}

	return cfg, nil
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

func v2DurationSetting(value interface{}) (time.Duration, bool) {
	if seconds, ok := v2IntegerSetting(value); ok {
		return time.Duration(seconds) * time.Second, true
	}
	switch v := value.(type) {
	case time.Duration:
		return v, true
	case string:
		if v == "" {
			return 0, false
		}
		d, err := time.ParseDuration(v)
		return d, err == nil
	case float64:
		return time.Duration(v * float64(time.Second)), true
	default:
		return 0, false
	}
}

func v2IntegerSetting(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		if uint64(v) > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}
