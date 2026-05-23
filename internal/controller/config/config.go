package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
)

// Config represents the controller configuration
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	DataStore DataStoreConfig `yaml:"datastore"`
	GRPC      GRPCConfig      `yaml:"grpc"`
	Log       LogConfig       `yaml:"log"`
}

// ServerConfig represents the REST API server configuration
type ServerConfig struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	APIKey            string        `yaml:"api_key"`
	APIKeyFile        string        `yaml:"api_key_file"`
	TLS               bool          `yaml:"tls"`
	CertFile          string        `yaml:"cert_file"`
	KeyFile           string        `yaml:"key_file"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes"`
	MaxBodyBytes      int64         `yaml:"max_body_bytes"`
	AllowedOrigins    []string      `yaml:"allowed_origins"` // CORS allowed origins
}

// DataStoreConfig represents the datastore configuration
type DataStoreConfig struct {
	Type string `yaml:"type"` // "etcd" or "mysql"

	// etcd settings
	Etcd EtcdConfig `yaml:"etcd"`

	// MySQL settings
	MySQL MySQLConfig `yaml:"mysql"`
}

// EtcdConfig represents etcd-specific configuration
type EtcdConfig struct {
	Endpoints      []string      `yaml:"endpoints"`
	KeyPrefix      string        `yaml:"key_prefix"`
	TLS            bool          `yaml:"tls"`
	CertFile       string        `yaml:"cert_file"`
	KeyFile        string        `yaml:"key_file"`
	CAFile         string        `yaml:"ca_file"`
	DialTimeout    time.Duration `yaml:"dial_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// MySQLConfig represents MySQL-specific configuration
type MySQLConfig struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	User              string        `yaml:"user"`
	Password          string        `yaml:"password"`
	PasswordFile      string        `yaml:"password_file"`
	Database          string        `yaml:"database"`
	TLSMode           string        `yaml:"tls_mode"`
	TLSCAFile         string        `yaml:"tls_ca_file"`
	TLSCertFile       string        `yaml:"tls_cert_file"`
	TLSKeyFile        string        `yaml:"tls_key_file"`
	TLSServerName     string        `yaml:"tls_server_name"`
	MaxOpenConns      int           `yaml:"max_open_conns"`
	MaxIdleConns      int           `yaml:"max_idle_conns"`
	ConnMaxLifetime   time.Duration `yaml:"conn_max_lifetime"`
	WatchPollInterval time.Duration `yaml:"watch_poll_interval"`
}

// GRPCConfig represents the gRPC server configuration
type GRPCConfig struct {
	Host                           string `yaml:"host"`
	Port                           int    `yaml:"port"`
	APIKey                         string `yaml:"api_key"`
	APIKeyFile                     string `yaml:"api_key_file"`
	TLS                            bool   `yaml:"tls"`
	CertFile                       string `yaml:"cert_file"`
	KeyFile                        string `yaml:"key_file"`
	ClientCAFile                   string `yaml:"client_ca_file"`
	RequireClientCert              bool   `yaml:"require_client_cert"`
	AuthorizeAgentIDWithClientCert bool   `yaml:"authorize_agent_id_with_client_cert"`
}

// LogConfig represents logging configuration
type LogConfig struct {
	Level  string `yaml:"level"`  // "debug", "info", "warn", "error"
	Format string `yaml:"format"` // "json" or "text"
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := decodeStrictYAML(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	cfg.setDefaults()

	if err := cfg.loadSecrets(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// setDefaults sets default values for configuration
func (c *Config) setDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 10 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 10 * time.Second
	}
	if c.Server.ReadHeaderTimeout == 0 {
		c.Server.ReadHeaderTimeout = 5 * time.Second
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = 60 * time.Second
	}
	if c.Server.MaxHeaderBytes == 0 {
		c.Server.MaxHeaderBytes = 1 << 20 // 1 MB
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 1 << 20 // 1 MB
	}
	if len(c.Server.AllowedOrigins) == 0 {
		c.Server.AllowedOrigins = []string{"http://localhost:3000"} // Default for development
	}

	if c.GRPC.Host == "" {
		c.GRPC.Host = "127.0.0.1"
	}
	if c.GRPC.Port == 0 {
		c.GRPC.Port = 50051
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
}

func (c *Config) loadSecrets() error {
	value, err := loadSecretValue("server.api_key", c.Server.APIKey, c.Server.APIKeyFile)
	if err != nil {
		return err
	}
	c.Server.APIKey = value

	value, err = loadSecretValue("grpc.api_key", c.GRPC.APIKey, c.GRPC.APIKeyFile)
	if err != nil {
		return err
	}
	c.GRPC.APIKey = value

	value, err = loadSecretValue("datastore.mysql.password", c.DataStore.MySQL.Password, c.DataStore.MySQL.PasswordFile)
	if err != nil {
		return err
	}
	c.DataStore.MySQL.Password = value

	return nil
}

func loadSecretValue(field, value, filePath string) (string, error) {
	if value != "" && filePath != "" {
		return "", fmt.Errorf("%s and %s_file must not both be set", field, field)
	}
	if filePath == "" {
		return value, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s_file: %w", field, err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if err := validateAPIKey("server.api_key", c.Server.APIKey); err != nil {
		return err
	}
	if c.Server.APIKey != "" && !c.Server.TLS {
		return fmt.Errorf("server.tls must be enabled when server.api_key is set")
	}
	if !c.Server.TLS {
		if c.Server.CertFile != "" {
			return fmt.Errorf("server.tls must be enabled when server.cert_file is set")
		}
		if c.Server.KeyFile != "" {
			return fmt.Errorf("server.tls must be enabled when server.key_file is set")
		}
	}
	if c.Server.TLS {
		if c.Server.CertFile == "" {
			return fmt.Errorf("server.cert_file is required when server.tls is enabled")
		}
		if c.Server.KeyFile == "" {
			return fmt.Errorf("server.key_file is required when server.tls is enabled")
		}
	}
	if !isLoopbackBindHost(c.Server.Host) {
		if !c.Server.TLS {
			return fmt.Errorf("server.tls must be enabled when server.host is non-loopback")
		}
		if c.Server.APIKey == "" {
			return fmt.Errorf("server.api_key or server.api_key_file must be set when server.host is non-loopback")
		}
	}
	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("server.write_timeout must be positive")
	}
	if c.Server.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("server.read_header_timeout must be positive")
	}
	if c.Server.IdleTimeout <= 0 {
		return fmt.Errorf("server.idle_timeout must be positive")
	}
	if c.Server.MaxHeaderBytes <= 0 {
		return fmt.Errorf("server.max_header_bytes must be positive")
	}
	if c.Server.MaxBodyBytes <= 0 {
		return fmt.Errorf("server.max_body_bytes must be positive")
	}
	if err := validateAllowedOrigins(c.Server.AllowedOrigins); err != nil {
		return err
	}
	if err := c.validateDataStore(); err != nil {
		return err
	}
	if c.GRPC.Port < 1 || c.GRPC.Port > 65535 {
		return fmt.Errorf("grpc.port must be between 1 and 65535")
	}
	if err := validateAPIKey("grpc.api_key", c.GRPC.APIKey); err != nil {
		return err
	}
	if c.GRPC.APIKey != "" && !c.GRPC.TLS {
		return fmt.Errorf("grpc.tls must be enabled when grpc.api_key is set")
	}
	if c.GRPC.RequireClientCert && !c.GRPC.TLS {
		return fmt.Errorf("grpc.tls must be enabled when grpc.require_client_cert is enabled")
	}
	if c.GRPC.AuthorizeAgentIDWithClientCert && !c.GRPC.RequireClientCert {
		return fmt.Errorf("grpc.require_client_cert must be enabled when grpc.authorize_agent_id_with_client_cert is enabled")
	}
	if !c.GRPC.TLS {
		if c.GRPC.CertFile != "" {
			return fmt.Errorf("grpc.tls must be enabled when grpc.cert_file is set")
		}
		if c.GRPC.KeyFile != "" {
			return fmt.Errorf("grpc.tls must be enabled when grpc.key_file is set")
		}
		if c.GRPC.ClientCAFile != "" {
			return fmt.Errorf("grpc.tls must be enabled when grpc.client_ca_file is set")
		}
	}
	if c.GRPC.TLS {
		if c.GRPC.CertFile == "" {
			return fmt.Errorf("grpc.cert_file is required when grpc.tls is enabled")
		}
		if c.GRPC.KeyFile == "" {
			return fmt.Errorf("grpc.key_file is required when grpc.tls is enabled")
		}
	}
	if c.GRPC.RequireClientCert && c.GRPC.ClientCAFile == "" {
		return fmt.Errorf("grpc.client_ca_file is required when grpc.require_client_cert is enabled")
	}
	if !isLoopbackBindHost(c.GRPC.Host) {
		if !c.GRPC.TLS {
			return fmt.Errorf("grpc.tls must be enabled when grpc.host is non-loopback")
		}
		if c.GRPC.APIKey == "" && !c.GRPC.RequireClientCert {
			return fmt.Errorf("grpc.api_key, grpc.api_key_file, or grpc.require_client_cert must be set when grpc.host is non-loopback")
		}
	}
	return nil
}

func isLoopbackBindHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
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

func validateAllowedOrigins(origins []string) error {
	for _, origin := range origins {
		if origin == "" || strings.ContainsAny(origin, " \t\r\n") {
			return fmt.Errorf("server.allowed_origins must not contain empty or whitespace values")
		}
		if origin == "*" {
			return fmt.Errorf("server.allowed_origins must not contain wildcard origins when credentials are enabled")
		}

		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
			return fmt.Errorf("server.allowed_origins contains invalid origin %q", origin)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("server.allowed_origins origin %q must use http or https", origin)
		}
		if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("server.allowed_origins contains invalid origin %q", origin)
		}
	}
	return nil
}

func (c *Config) validateDataStore() error {
	switch c.DataStore.Type {
	case "":
		return fmt.Errorf("datastore.type is required")
	case "etcd":
		return validateEtcdConfig(c.DataStore.Etcd)
	case "mysql":
		return validateMySQLConfig(c.DataStore.MySQL)
	default:
		return fmt.Errorf("unsupported datastore.type: %s", c.DataStore.Type)
	}
}

func validateEtcdConfig(cfg EtcdConfig) error {
	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("datastore.etcd.endpoints is required")
	}
	for _, endpoint := range cfg.Endpoints {
		if endpoint == "" {
			return fmt.Errorf("datastore.etcd.endpoints must not contain empty values")
		}
	}
	if cfg.DialTimeout < 0 {
		return fmt.Errorf("datastore.etcd.dial_timeout must be non-negative")
	}
	if cfg.RequestTimeout < 0 {
		return fmt.Errorf("datastore.etcd.request_timeout must be non-negative")
	}
	if !cfg.TLS {
		if cfg.CAFile != "" {
			return fmt.Errorf("datastore.etcd.tls must be enabled when datastore.etcd.ca_file is set")
		}
		if cfg.CertFile != "" {
			return fmt.Errorf("datastore.etcd.tls must be enabled when datastore.etcd.cert_file is set")
		}
		if cfg.KeyFile != "" {
			return fmt.Errorf("datastore.etcd.tls must be enabled when datastore.etcd.key_file is set")
		}
	}
	if cfg.TLS {
		if cfg.CAFile == "" {
			return fmt.Errorf("datastore.etcd.ca_file is required when datastore.etcd.tls is enabled")
		}
		if (cfg.CertFile == "") != (cfg.KeyFile == "") {
			return fmt.Errorf("datastore.etcd.cert_file and datastore.etcd.key_file must both be set when client certificate is configured")
		}
	}
	return nil
}

func validateMySQLConfig(cfg MySQLConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("datastore.mysql.host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("datastore.mysql.port must be between 1 and 65535")
	}
	if cfg.User == "" {
		return fmt.Errorf("datastore.mysql.user is required")
	}
	if cfg.Database == "" {
		return fmt.Errorf("datastore.mysql.database is required")
	}
	if err := validateMySQLTLSConfig(cfg); err != nil {
		return err
	}
	if cfg.MaxOpenConns < 0 {
		return fmt.Errorf("datastore.mysql.max_open_conns must be non-negative")
	}
	if cfg.MaxIdleConns < 0 {
		return fmt.Errorf("datastore.mysql.max_idle_conns must be non-negative")
	}
	if cfg.MaxOpenConns > 0 && cfg.MaxIdleConns > cfg.MaxOpenConns {
		return fmt.Errorf("datastore.mysql.max_idle_conns must not exceed datastore.mysql.max_open_conns")
	}
	if cfg.ConnMaxLifetime < 0 {
		return fmt.Errorf("datastore.mysql.conn_max_lifetime must be non-negative")
	}
	if cfg.WatchPollInterval < 0 {
		return fmt.Errorf("datastore.mysql.watch_poll_interval must be non-negative")
	}
	return nil
}

func validateMySQLTLSConfig(cfg MySQLConfig) error {
	switch cfg.TLSMode {
	case "", "false", "true", "skip-verify", "preferred", "custom":
	default:
		return fmt.Errorf("datastore.mysql.tls_mode must be one of false, true, skip-verify, preferred, or custom")
	}

	hasCustomTLSFields := cfg.TLSCAFile != "" || cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.TLSServerName != ""
	if hasCustomTLSFields && cfg.TLSMode != "custom" {
		return fmt.Errorf("datastore.mysql.tls_mode must be custom when MySQL TLS certificate fields are set")
	}
	if cfg.TLSMode == "custom" {
		if !hasCustomTLSFields {
			return fmt.Errorf("datastore.mysql.tls_mode custom requires at least one MySQL TLS certificate field")
		}
		if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
			return fmt.Errorf("datastore.mysql.tls_cert_file and datastore.mysql.tls_key_file must both be set when client certificate is configured")
		}
	}
	return nil
}

// ToDataStoreConfig converts to datastore.Config
func (c *Config) ToDataStoreConfig() *datastore.Config {
	cfg := &datastore.Config{
		Type: c.DataStore.Type,
	}

	switch c.DataStore.Type {
	case "etcd":
		cfg.EtcdEndpoints = c.DataStore.Etcd.Endpoints
		cfg.EtcdKeyPrefix = c.DataStore.Etcd.KeyPrefix
		cfg.EtcdTLS = c.DataStore.Etcd.TLS
		cfg.EtcdCertFile = c.DataStore.Etcd.CertFile
		cfg.EtcdKeyFile = c.DataStore.Etcd.KeyFile
		cfg.EtcdCAFile = c.DataStore.Etcd.CAFile
		cfg.EtcdDialTimeout = c.DataStore.Etcd.DialTimeout
		cfg.EtcdRequestTimeout = c.DataStore.Etcd.RequestTimeout
	case "mysql":
		cfg.MySQLHost = c.DataStore.MySQL.Host
		cfg.MySQLPort = c.DataStore.MySQL.Port
		cfg.MySQLUser = c.DataStore.MySQL.User
		cfg.MySQLPassword = c.DataStore.MySQL.Password
		cfg.MySQLDatabase = c.DataStore.MySQL.Database
		cfg.MySQLTLSMode = c.DataStore.MySQL.TLSMode
		cfg.MySQLTLSCAFile = c.DataStore.MySQL.TLSCAFile
		cfg.MySQLTLSCertFile = c.DataStore.MySQL.TLSCertFile
		cfg.MySQLTLSKeyFile = c.DataStore.MySQL.TLSKeyFile
		cfg.MySQLTLSServerName = c.DataStore.MySQL.TLSServerName
		cfg.MySQLMaxOpenConns = c.DataStore.MySQL.MaxOpenConns
		cfg.MySQLMaxIdleConns = c.DataStore.MySQL.MaxIdleConns
		cfg.MySQLConnMaxLifetime = c.DataStore.MySQL.ConnMaxLifetime
		cfg.MySQLWatchPollInterval = c.DataStore.MySQL.WatchPollInterval
	}

	return cfg
}
