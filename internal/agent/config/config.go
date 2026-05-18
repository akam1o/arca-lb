package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the agent configuration
type Config struct {
	Agent       AgentConfig       `yaml:"agent"`
	Controller  ControllerConfig  `yaml:"controller"`
	VPP         VPPConfig         `yaml:"vpp"`
	FRR         FRRConfig         `yaml:"frr"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
	Log         LogConfig         `yaml:"log"`
	Metrics     MetricsConfig     `yaml:"metrics"`
}

// AgentConfig contains agent-specific settings
type AgentConfig struct {
	// ID is a unique identifier for this agent (defaults to hostname)
	ID string `yaml:"id"`

	// Metadata contains additional agent information
	Metadata map[string]string `yaml:"metadata"`

	// Version is the agent version (set by build)
	Version string `yaml:"version"`

	// ReconcileInterval is how often to check for configuration drift
	ReconcileInterval time.Duration `yaml:"reconcile_interval"`

	// HeartbeatInterval is how often to send heartbeats to controller
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
}

// ControllerConfig contains controller connection settings
type ControllerConfig struct {
	// Address is the controller gRPC endpoint
	Address string `yaml:"address"`

	// APIKey is an optional bearer token for controller gRPC authentication
	APIKey string `yaml:"api_key"`

	// TLS settings
	TLS TLSConfig `yaml:"tls"`

	// Timeout for gRPC calls
	Timeout time.Duration `yaml:"timeout"`

	// MaxRetries for connection attempts
	MaxRetries int `yaml:"max_retries"`

	// RetryBackoff is the initial backoff duration for retries
	RetryBackoff time.Duration `yaml:"retry_backoff"`

	// MaxRetryBackoff is the maximum backoff duration
	MaxRetryBackoff time.Duration `yaml:"max_retry_backoff"`
}

// TLSConfig contains TLS settings
type TLSConfig struct {
	// Enabled determines if TLS should be used
	Enabled bool `yaml:"enabled"`

	// CertFile is the path to the client certificate
	CertFile string `yaml:"cert_file"`

	// KeyFile is the path to the client key
	KeyFile string `yaml:"key_file"`

	// CAFile is the path to the CA certificate
	CAFile string `yaml:"ca_file"`

	// InsecureSkipVerify skips server certificate verification
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// VPPConfig contains VPP connection settings
type VPPConfig struct {
	// SocketPath is the path to VPP API socket
	SocketPath string `yaml:"socket_path"`

	// ConnectTimeout is the timeout for initial connection
	ConnectTimeout time.Duration `yaml:"connect_timeout"`

	// ReconnectInterval is how often to retry connection
	ReconnectInterval time.Duration `yaml:"reconnect_interval"`

	// MaxReconnectAttempts is the maximum number of reconnection attempts
	// 0 means infinite retries
	MaxReconnectAttempts int `yaml:"max_reconnect_attempts"`

	// LB contains VPP load balancer parameters
	LB VPPLBConfig `yaml:"lb"`
}

// VPPLBConfig contains VPP load balancer configuration parameters
type VPPLBConfig struct {
	// EncapType is the encapsulation type for load balancing
	// Supported values: GRE4, GRE6, L3DSR, NAT4, NAT6
	// Default: GRE4
	EncapType string `yaml:"encap_type"`

	// DSCP is the default DSCP value for DSCP-based L3DSR.
	// Valid range: 1-63 when using L3DSR with DSCP steering.
	// Default: 0
	DSCP uint8 `yaml:"dscp"`

	// Type is the load balancer service type
	// Supported values: CLUSTERIP, NODEPORT
	// Default: CLUSTERIP
	Type string `yaml:"type"`

	// NewFlowsTableLength is the flow table size for new connections
	// Larger values provide better hashing distribution but use more memory
	// Default: 1024
	NewFlowsTableLength uint32 `yaml:"new_flows_table_length"`

	// FailOnAllBackendsDown determines if VIP creation should fail when all backends fail to add
	// If true, VIP creation fails if no backends can be added
	// If false, VIP is created even with no working backends (backends can be added later)
	// Default: false (allow VIP creation without backends)
	FailOnAllBackendsDown bool `yaml:"fail_on_all_backends_down"`
}

// FRRConfig contains FRR integration settings
type FRRConfig struct {
	// Enabled determines if FRR integration is enabled
	Enabled bool `yaml:"enabled"`

	// VTYShell is the path to vtysh command
	VTYShell string `yaml:"vtysh"`

	// ConfigFile is the path to FRR configuration
	ConfigFile string `yaml:"config_file"`
}

// HealthCheckConfig contains health check settings
type HealthCheckConfig struct {
	// WorkerCount is the number of concurrent health check workers
	WorkerCount int `yaml:"worker_count"`

	// DefaultTimeout is the default timeout for health checks
	DefaultTimeout time.Duration `yaml:"default_timeout"`

	// MaxConcurrentChecks limits concurrent health checks per worker
	MaxConcurrentChecks int `yaml:"max_concurrent_checks"`
}

// LogConfig contains logging settings
type LogConfig struct {
	// Level is the log level (debug, info, warn, error)
	Level string `yaml:"level"`

	// Format is the log format (json, text)
	Format string `yaml:"format"`

	// Output is the log output (stdout, stderr, or file path)
	Output string `yaml:"output"`
}

// MetricsConfig contains metrics settings
type MetricsConfig struct {
	// Enabled determines if metrics should be collected
	Enabled bool `yaml:"enabled"`

	// ListenAddress is the address to listen on for metrics
	ListenAddress string `yaml:"listen_address"`

	// Path is the HTTP path for metrics endpoint
	Path string `yaml:"path"`

	// Timeout for metrics HTTP server operations
	Timeout time.Duration `yaml:"timeout"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(&config)

	// Apply defaults
	applyDefaults(&config)

	// Validate configuration
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// applyEnvOverrides applies environment variable overrides
func applyEnvOverrides(cfg *Config) {
	// Agent ID
	if v := os.Getenv("ARCA_AGENT_ID"); v != "" {
		cfg.Agent.ID = v
	}

	// Controller address
	if v := os.Getenv("ARCA_CONTROLLER_ADDRESS"); v != "" {
		cfg.Controller.Address = v
	}
	if v := os.Getenv("ARCA_CONTROLLER_API_KEY"); v != "" {
		cfg.Controller.APIKey = v
	}

	// VPP socket path
	if v := os.Getenv("ARCA_VPP_SOCKET"); v != "" {
		cfg.VPP.SocketPath = v
	}

	// Log level
	if v := os.Getenv("ARCA_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}

	// Log format
	if v := os.Getenv("ARCA_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}

	// TLS settings
	if v := os.Getenv("ARCA_TLS_ENABLED"); v != "" {
		cfg.Controller.TLS.Enabled = v == "true"
	}
	if v := os.Getenv("ARCA_TLS_CERT"); v != "" {
		cfg.Controller.TLS.CertFile = v
	}
	if v := os.Getenv("ARCA_TLS_KEY"); v != "" {
		cfg.Controller.TLS.KeyFile = v
	}
	if v := os.Getenv("ARCA_TLS_CA"); v != "" {
		cfg.Controller.TLS.CAFile = v
	}
}

// applyDefaults applies default values to configuration
func applyDefaults(cfg *Config) {
	// Agent defaults
	if cfg.Agent.ID == "" {
		// Use hostname as default agent ID
		if hostname, err := os.Hostname(); err == nil {
			cfg.Agent.ID = hostname
		} else {
			cfg.Agent.ID = "agent-unknown"
		}
	}
	if cfg.Agent.ReconcileInterval == 0 {
		cfg.Agent.ReconcileInterval = 30 * time.Second
	}
	if cfg.Agent.HeartbeatInterval == 0 {
		cfg.Agent.HeartbeatInterval = 10 * time.Second
	}

	// Controller defaults
	if cfg.Controller.Address == "" {
		cfg.Controller.Address = "localhost:50051"
	}
	if cfg.Controller.Timeout == 0 {
		cfg.Controller.Timeout = 10 * time.Second
	}
	if cfg.Controller.MaxRetries == 0 {
		cfg.Controller.MaxRetries = 5
	}
	if cfg.Controller.RetryBackoff == 0 {
		cfg.Controller.RetryBackoff = 1 * time.Second
	}
	if cfg.Controller.MaxRetryBackoff == 0 {
		cfg.Controller.MaxRetryBackoff = 30 * time.Second
	}

	// VPP defaults
	if cfg.VPP.SocketPath == "" {
		cfg.VPP.SocketPath = "/run/vpp/api.sock"
	}
	if cfg.VPP.ConnectTimeout == 0 {
		cfg.VPP.ConnectTimeout = 5 * time.Second
	}
	if cfg.VPP.ReconnectInterval == 0 {
		cfg.VPP.ReconnectInterval = 5 * time.Second
	}

	// VPP LB defaults
	if cfg.VPP.LB.EncapType == "" {
		cfg.VPP.LB.EncapType = "GRE4"
	}
	if cfg.VPP.LB.Type == "" {
		cfg.VPP.LB.Type = "CLUSTERIP"
	}
	if cfg.VPP.LB.NewFlowsTableLength == 0 {
		cfg.VPP.LB.NewFlowsTableLength = 1024
	}

	// FRR defaults
	if cfg.FRR.VTYShell == "" {
		cfg.FRR.VTYShell = "/usr/bin/vtysh"
	}
	if cfg.FRR.ConfigFile == "" {
		cfg.FRR.ConfigFile = "/etc/frr/frr.conf"
	}

	// Health check defaults
	if cfg.HealthCheck.WorkerCount == 0 {
		cfg.HealthCheck.WorkerCount = 4
	}
	if cfg.HealthCheck.DefaultTimeout == 0 {
		cfg.HealthCheck.DefaultTimeout = 3 * time.Second
	}
	if cfg.HealthCheck.MaxConcurrentChecks == 0 {
		cfg.HealthCheck.MaxConcurrentChecks = 100
	}

	// Log defaults
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	if cfg.Log.Output == "" {
		cfg.Log.Output = "stdout"
	}

	// Metrics defaults
	if cfg.Metrics.ListenAddress == "" {
		cfg.Metrics.ListenAddress = "0.0.0.0:9090"
	}
	if cfg.Metrics.Path == "" {
		cfg.Metrics.Path = "/metrics"
	}
	if cfg.Metrics.Timeout == 0 {
		cfg.Metrics.Timeout = 10 * time.Second
	}
}

// validate validates the configuration
func validate(cfg *Config) error {
	// Validate agent ID
	if strings.TrimSpace(cfg.Agent.ID) == "" {
		return fmt.Errorf("agent.id is required")
	}

	// Validate controller address
	if cfg.Controller.Address == "" {
		return fmt.Errorf("controller.address is required")
	}
	if err := validateAPIKey("controller.api_key", cfg.Controller.APIKey); err != nil {
		return err
	}
	if cfg.Controller.APIKey != "" && !cfg.Controller.TLS.Enabled {
		return fmt.Errorf("controller.tls.enabled must be enabled when controller.api_key is set")
	}

	// Validate VPP socket path
	if cfg.VPP.SocketPath == "" {
		return fmt.Errorf("vpp.socket_path is required")
	}

	// Validate log level
	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true,
	}
	if !validLevels[cfg.Log.Level] {
		return fmt.Errorf("invalid log level: %s", cfg.Log.Level)
	}

	// Validate log format
	validFormats := map[string]bool{
		"json": true, "text": true,
	}
	if !validFormats[cfg.Log.Format] {
		return fmt.Errorf("invalid log format: %s", cfg.Log.Format)
	}

	// Validate TLS settings
	if cfg.Controller.TLS.Enabled {
		if cfg.Controller.TLS.CAFile == "" {
			return fmt.Errorf("tls.ca_file is required when TLS is enabled")
		}
		if (cfg.Controller.TLS.CertFile == "") != (cfg.Controller.TLS.KeyFile == "") {
			return fmt.Errorf("tls.cert_file and tls.key_file must both be set when client certificate is configured")
		}
	}

	// Validate intervals
	if cfg.Agent.ReconcileInterval <= 0 {
		return fmt.Errorf("agent.reconcile_interval must be positive")
	}
	if cfg.Agent.HeartbeatInterval <= 0 {
		return fmt.Errorf("agent.heartbeat_interval must be positive")
	}

	// Validate controller timeouts and retries
	if cfg.Controller.Timeout <= 0 {
		return fmt.Errorf("controller.timeout must be positive")
	}
	if cfg.Controller.MaxRetries <= 0 {
		return fmt.Errorf("controller.max_retries must be positive")
	}
	if cfg.Controller.RetryBackoff <= 0 {
		return fmt.Errorf("controller.retry_backoff must be positive")
	}
	if cfg.Controller.MaxRetryBackoff <= 0 {
		return fmt.Errorf("controller.max_retry_backoff must be positive")
	}
	if cfg.Controller.MaxRetryBackoff < cfg.Controller.RetryBackoff {
		return fmt.Errorf("controller.max_retry_backoff must be >= retry_backoff")
	}

	// Validate VPP timeouts
	if cfg.VPP.ConnectTimeout <= 0 {
		return fmt.Errorf("vpp.connect_timeout must be positive")
	}
	if cfg.VPP.ReconnectInterval <= 0 {
		return fmt.Errorf("vpp.reconnect_interval must be positive")
	}
	if cfg.VPP.MaxReconnectAttempts < 0 {
		return fmt.Errorf("vpp.max_reconnect_attempts must be non-negative (0 = infinite)")
	}

	// Validate VPP LB settings (only if values are set after defaults are applied)
	if cfg.VPP.LB.EncapType != "" {
		validEncapTypes := map[string]bool{
			"GRE4": true, "GRE6": true, "L3DSR": true, "NAT4": true, "NAT6": true,
		}
		if !validEncapTypes[cfg.VPP.LB.EncapType] {
			return fmt.Errorf("invalid vpp.lb.encap_type: %s (must be one of: GRE4, GRE6, L3DSR, NAT4, NAT6)", cfg.VPP.LB.EncapType)
		}
	}

	if cfg.VPP.LB.Type != "" {
		validLBTypes := map[string]bool{
			"CLUSTERIP": true, "NODEPORT": true,
		}
		if !validLBTypes[cfg.VPP.LB.Type] {
			return fmt.Errorf("invalid vpp.lb.type: %s (must be one of: CLUSTERIP, NODEPORT)", cfg.VPP.LB.Type)
		}
	}

	if cfg.VPP.LB.DSCP > 63 {
		return fmt.Errorf("vpp.lb.dscp must be <= 63")
	}

	if cfg.VPP.LB.NewFlowsTableLength > 0 && !isPowerOfTwo(int64(cfg.VPP.LB.NewFlowsTableLength)) {
		return fmt.Errorf("vpp.lb.new_flows_table_length must be a power of two if set")
	}

	// Validate health check settings
	if cfg.HealthCheck.WorkerCount <= 0 {
		return fmt.Errorf("health_check.worker_count must be positive")
	}
	if cfg.HealthCheck.DefaultTimeout <= 0 {
		return fmt.Errorf("health_check.default_timeout must be positive")
	}
	if cfg.HealthCheck.MaxConcurrentChecks <= 0 {
		return fmt.Errorf("health_check.max_concurrent_checks must be positive")
	}

	return nil
}

func validateAPIKey(field, value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s must not contain whitespace", field)
	}
	if len(value) < 16 {
		return fmt.Errorf("%s must be at least 16 characters when set", field)
	}
	return nil
}
