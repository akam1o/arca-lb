package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigRequiresGRPCTLSFiles(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: true
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.cert_file is required") {
		t.Fatalf("LoadConfig error = %v, want grpc.cert_file validation error", err)
	}
}

func TestLoadConfigRequiresGRPCClientCAWhenClientCertRequired(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
  require_client_cert: true
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.client_ca_file is required") {
		t.Fatalf("LoadConfig error = %v, want grpc.client_ca_file validation error", err)
	}
}

func TestLoadConfigRejectsGRPCTLSFilesWithoutTLS(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "cert file",
			yaml: `
grpc:
  cert_file: /tmp/server.crt
`,
			wantErr: "grpc.tls must be enabled when grpc.cert_file is set",
		},
		{
			name: "key file",
			yaml: `
grpc:
  key_file: /tmp/server.key
`,
			wantErr: "grpc.tls must be enabled when grpc.key_file is set",
		},
		{
			name: "client CA file",
			yaml: `
grpc:
  client_ca_file: /tmp/ca.crt
`,
			wantErr: "grpc.tls must be enabled when grpc.client_ca_file is set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, minimalEtcdConfig()+tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  api_kee: controller-rest-secret
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "field api_kee not found") {
		t.Fatalf("LoadConfig error = %v, want unknown field error", err)
	}
}

func TestLoadConfigRejectsDuplicateYAMLKeys(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  api_key: first-controller-secret
  api_key: second-controller-secret
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), `duplicate yaml key "server.api_key"`) {
		t.Fatalf("LoadConfig error = %v, want duplicate yaml key error", err)
	}
}

func TestLoadConfigRejectsMultipleYAMLDocuments(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
---
server:
  api_key: controller-rest-secret
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "multiple yaml documents are not supported") {
		t.Fatalf("LoadConfig error = %v, want multiple yaml document error", err)
	}
}

func TestLoadConfigRejectsGRPCClientCertWithoutTLS(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: false
  require_client_cert: true
  client_ca_file: /tmp/ca.crt
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.tls must be enabled") {
		t.Fatalf("LoadConfig error = %v, want grpc.tls validation error", err)
	}
}

func TestLoadConfigRejectsGRPCAgentIDClientCertAuthorizationWithoutClientCert(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
  authorize_agent_id_with_client_cert: true
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.require_client_cert must be enabled when grpc.authorize_agent_id_with_client_cert is enabled") {
		t.Fatalf("LoadConfig error = %v, want grpc.authorize_agent_id_with_client_cert validation error", err)
	}
}

func TestLoadConfigAcceptsGRPCAgentIDClientCertAuthorization(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
  require_client_cert: true
  client_ca_file: /tmp/ca.crt
  authorize_agent_id_with_client_cert: true
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.GRPC.AuthorizeAgentIDWithClientCert {
		t.Fatal("GRPC.AuthorizeAgentIDWithClientCert = false, want true")
	}
}

func TestLoadConfigRejectsInvalidGRPCAPIKey(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "short",
			yaml: "grpc:\n  api_key: short\n",
		},
		{
			name: "whitespace",
			yaml: "grpc:\n  api_key: controller grpc secret\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, minimalEtcdConfig()+tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), "grpc.api_key") {
				t.Fatalf("LoadConfig error = %v, want grpc.api_key validation error", err)
			}
		})
	}
}

func TestLoadConfigRejectsServerAPIKeyWithoutTLS(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  api_key: controller-rest-secret
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "server.tls must be enabled when server.api_key is set") {
		t.Fatalf("LoadConfig error = %v, want server.api_key TLS validation error", err)
	}
}

func TestLoadConfigRequiresServerTLSFiles(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing cert",
			yaml: `
server:
  tls: true
`,
			wantErr: "server.cert_file is required",
		},
		{
			name: "missing key",
			yaml: `
server:
  tls: true
  cert_file: /tmp/server.crt
`,
			wantErr: "server.key_file is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, minimalEtcdConfig()+tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigRejectsServerTLSFilesWithoutTLS(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "cert file",
			yaml: `
server:
  cert_file: /tmp/server.crt
`,
			wantErr: "server.tls must be enabled when server.cert_file is set",
		},
		{
			name: "key file",
			yaml: `
server:
  key_file: /tmp/server.key
`,
			wantErr: "server.tls must be enabled when server.key_file is set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, minimalEtcdConfig()+tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigAcceptsServerTLSWithAPIKey(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
  api_key: controller-rest-secret
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Server.TLS {
		t.Fatal("Server.TLS = false, want true")
	}
	if cfg.Server.CertFile != "/tmp/server.crt" {
		t.Fatalf("Server.CertFile = %q", cfg.Server.CertFile)
	}
	if cfg.Server.KeyFile != "/tmp/server.key" {
		t.Fatalf("Server.KeyFile = %q", cfg.Server.KeyFile)
	}
	if cfg.Server.APIKey != "controller-rest-secret" {
		t.Fatalf("Server.APIKey = %q", cfg.Server.APIKey)
	}
}

func TestLoadConfigLoadsSecretFileValues(t *testing.T) {
	dir := t.TempDir()
	restKeyFile := writeFile(t, dir, "rest-api-key", "controller-rest-secret\n")
	grpcKeyFile := writeFile(t, dir, "grpc-api-key", "controller-grpc-secret\r\n")
	mysqlPasswordFile := writeFile(t, dir, "mysql-password", "db-secret\n")

	path := writeConfigFile(t, `
server:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
  api_key_file: `+quoteYAMLString(restKeyFile)+`
datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    password_file: `+quoteYAMLString(mysqlPasswordFile)+`
    database: arcalb
grpc:
  tls: true
  cert_file: /tmp/grpc.crt
  key_file: /tmp/grpc.key
  api_key_file: `+quoteYAMLString(grpcKeyFile)+`
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Server.APIKey != "controller-rest-secret" {
		t.Fatalf("Server.APIKey = %q", cfg.Server.APIKey)
	}
	if cfg.GRPC.APIKey != "controller-grpc-secret" {
		t.Fatalf("GRPC.APIKey = %q", cfg.GRPC.APIKey)
	}
	if cfg.ToDataStoreConfig().MySQLPassword != "db-secret" {
		t.Fatalf("MySQLPassword = %q", cfg.ToDataStoreConfig().MySQLPassword)
	}
}

func TestLoadConfigRejectsConflictingSecretValues(t *testing.T) {
	secretFile := writeFile(t, t.TempDir(), "secret", "secret-value")

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "server api key",
			yaml: minimalEtcdConfig() + `
server:
  api_key: controller-rest-secret
  api_key_file: ` + quoteYAMLString(secretFile) + `
`,
			wantErr: "server.api_key and server.api_key_file must not both be set",
		},
		{
			name: "grpc api key",
			yaml: minimalEtcdConfig() + `
grpc:
  api_key: controller-grpc-secret
  api_key_file: ` + quoteYAMLString(secretFile) + `
`,
			wantErr: "grpc.api_key and grpc.api_key_file must not both be set",
		},
		{
			name: "mysql password",
			yaml: `
datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    password: direct-secret
    password_file: ` + quoteYAMLString(secretFile) + `
    database: arcalb
`,
			wantErr: "datastore.mysql.password and datastore.mysql.password_file must not both be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigRejectsGRPCAPIKeyWithoutTLS(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  api_key: controller-grpc-secret
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.tls must be enabled when grpc.api_key is set") {
		t.Fatalf("LoadConfig error = %v, want grpc.api_key TLS validation error", err)
	}
}

func TestLoadConfigAcceptsGRPCTLSFiles(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
grpc:
  tls: true
  cert_file: /tmp/server.crt
  key_file: /tmp/server.key
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.GRPC.TLS {
		t.Fatal("GRPC.TLS = false, want true")
	}
	if cfg.GRPC.CertFile != "/tmp/server.crt" {
		t.Fatalf("GRPC.CertFile = %q", cfg.GRPC.CertFile)
	}
	if cfg.GRPC.KeyFile != "/tmp/server.key" {
		t.Fatalf("GRPC.KeyFile = %q", cfg.GRPC.KeyFile)
	}
}

func TestLoadConfigDefaultsMaxBodyBytes(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig())

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Server.MaxBodyBytes != 1<<20 {
		t.Fatalf("Server.MaxBodyBytes = %d, want 1MiB", cfg.Server.MaxBodyBytes)
	}
}

func TestLoadConfigRejectsInvalidAllowedOrigins(t *testing.T) {
	tests := []struct {
		name   string
		origin string
	}{
		{
			name:   "wildcard",
			origin: "*",
		},
		{
			name:   "empty",
			origin: "",
		},
		{
			name:   "whitespace",
			origin: "https://example .com",
		},
		{
			name:   "unsupported scheme",
			origin: "file://example.com",
		},
		{
			name:   "path",
			origin: "https://example.com/app",
		},
		{
			name:   "userinfo",
			origin: "https://user@example.com",
		},
		{
			name:   "query",
			origin: "https://example.com?debug=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  allowed_origins:
    - `+quoteYAMLString(tt.origin)+`
`)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), "server.allowed_origins") {
				t.Fatalf("LoadConfig error = %v, want server.allowed_origins validation error", err)
			}
		})
	}
}

func TestLoadConfigAcceptsAllowedOriginWithPort(t *testing.T) {
	path := writeConfigFile(t, minimalEtcdConfig()+`
server:
  allowed_origins:
    - "https://example.com:8443"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Server.AllowedOrigins; len(got) != 1 || got[0] != "https://example.com:8443" {
		t.Fatalf("Server.AllowedOrigins = %v, want [https://example.com:8443]", got)
	}
}

func TestLoadConfigRejectsMissingDataStoreType(t *testing.T) {
	path := writeConfigFile(t, `{}`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "datastore.type") {
		t.Fatalf("LoadConfig error = %v, want datastore.type validation error", err)
	}
}

func TestLoadConfigRejectsInvalidDataStoreSettings(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "unsupported type",
			yaml:    "datastore:\n  type: sqlite\n",
			wantErr: "unsupported datastore.type",
		},
		{
			name:    "etcd missing endpoints",
			yaml:    "datastore:\n  type: etcd\n",
			wantErr: "datastore.etcd.endpoints",
		},
		{
			name: "etcd empty endpoint",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: [""]
`,
			wantErr: "datastore.etcd.endpoints",
		},
		{
			name: "etcd tls missing ca",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    tls: true
`,
			wantErr: "datastore.etcd.ca_file",
		},
		{
			name: "etcd tls partial client certificate",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    tls: true
    cert_file: /tmp/client.crt
    ca_file: /tmp/ca.crt
`,
			wantErr: "datastore.etcd.cert_file and datastore.etcd.key_file",
		},
		{
			name: "etcd ca without tls",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    ca_file: /tmp/ca.crt
`,
			wantErr: "datastore.etcd.tls must be enabled when datastore.etcd.ca_file is set",
		},
		{
			name: "etcd cert without tls",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    cert_file: /tmp/client.crt
`,
			wantErr: "datastore.etcd.tls must be enabled when datastore.etcd.cert_file is set",
		},
		{
			name: "etcd key without tls",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    key_file: /tmp/client.key
`,
			wantErr: "datastore.etcd.tls must be enabled when datastore.etcd.key_file is set",
		},
		{
			name: "etcd negative timeout",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    dial_timeout: -1s
`,
			wantErr: "datastore.etcd.dial_timeout",
		},
		{
			name: "mysql missing host",
			yaml: `datastore:
  type: mysql
  mysql:
    port: 3306
    database: arcalb
`,
			wantErr: "datastore.mysql.host",
		},
		{
			name: "mysql invalid port",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 70000
    user: arcalb
    database: arcalb
`,
			wantErr: "datastore.mysql.port",
		},
		{
			name: "mysql missing user",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    database: arcalb
`,
			wantErr: "datastore.mysql.user",
		},
		{
			name: "mysql missing database",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
`,
			wantErr: "datastore.mysql.database",
		},
		{
			name: "mysql invalid tls mode",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    tls_mode: strict
`,
			wantErr: "datastore.mysql.tls_mode",
		},
		{
			name: "mysql tls files require custom mode",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    tls_mode: true
    tls_ca_file: /tmp/ca.crt
`,
			wantErr: "datastore.mysql.tls_mode",
		},
		{
			name: "mysql custom tls requires custom field",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    tls_mode: custom
`,
			wantErr: "datastore.mysql.tls_mode custom",
		},
		{
			name: "mysql tls partial client certificate",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    tls_mode: custom
    tls_cert_file: /tmp/client.crt
`,
			wantErr: "datastore.mysql.tls_cert_file and datastore.mysql.tls_key_file",
		},
		{
			name: "mysql negative max open conns",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    max_open_conns: -1
`,
			wantErr: "datastore.mysql.max_open_conns",
		},
		{
			name: "mysql max idle exceeds max open",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    max_open_conns: 5
    max_idle_conns: 6
`,
			wantErr: "datastore.mysql.max_idle_conns",
		},
		{
			name: "mysql negative connection lifetime",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    conn_max_lifetime: -1s
`,
			wantErr: "datastore.mysql.conn_max_lifetime",
		},
		{
			name: "mysql negative watch poll interval",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
    user: arcalb
    database: arcalb
    watch_poll_interval: -1s
`,
			wantErr: "datastore.mysql.watch_poll_interval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigAcceptsMySQLOperationalSettings(t *testing.T) {
	path := writeConfigFile(t, `
datastore:
  type: mysql
  mysql:
    host: db.example.com
    port: 3307
    user: arcalb
    password: secret
    database: arcalb
    tls_mode: custom
    tls_ca_file: /etc/mysql/ca.pem
    tls_cert_file: /etc/mysql/client.pem
    tls_key_file: /etc/mysql/client-key.pem
    tls_server_name: mysql.internal.example
    max_open_conns: 50
    max_idle_conns: 10
    conn_max_lifetime: 10m
    watch_poll_interval: 250ms
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	dsCfg := cfg.ToDataStoreConfig()

	if dsCfg.MySQLHost != "db.example.com" {
		t.Fatalf("MySQLHost = %q, want db.example.com", dsCfg.MySQLHost)
	}
	if dsCfg.MySQLPort != 3307 {
		t.Fatalf("MySQLPort = %d, want 3307", dsCfg.MySQLPort)
	}
	if dsCfg.MySQLTLSMode != "custom" {
		t.Fatalf("MySQLTLSMode = %q, want custom", dsCfg.MySQLTLSMode)
	}
	if dsCfg.MySQLTLSCAFile != "/etc/mysql/ca.pem" {
		t.Fatalf("MySQLTLSCAFile = %q", dsCfg.MySQLTLSCAFile)
	}
	if dsCfg.MySQLTLSCertFile != "/etc/mysql/client.pem" {
		t.Fatalf("MySQLTLSCertFile = %q", dsCfg.MySQLTLSCertFile)
	}
	if dsCfg.MySQLTLSKeyFile != "/etc/mysql/client-key.pem" {
		t.Fatalf("MySQLTLSKeyFile = %q", dsCfg.MySQLTLSKeyFile)
	}
	if dsCfg.MySQLTLSServerName != "mysql.internal.example" {
		t.Fatalf("MySQLTLSServerName = %q", dsCfg.MySQLTLSServerName)
	}
	if dsCfg.MySQLMaxOpenConns != 50 {
		t.Fatalf("MySQLMaxOpenConns = %d, want 50", dsCfg.MySQLMaxOpenConns)
	}
	if dsCfg.MySQLMaxIdleConns != 10 {
		t.Fatalf("MySQLMaxIdleConns = %d, want 10", dsCfg.MySQLMaxIdleConns)
	}
	if dsCfg.MySQLConnMaxLifetime != 10*time.Minute {
		t.Fatalf("MySQLConnMaxLifetime = %s, want 10m", dsCfg.MySQLConnMaxLifetime)
	}
	if dsCfg.MySQLWatchPollInterval != 250*time.Millisecond {
		t.Fatalf("MySQLWatchPollInterval = %s, want 250ms", dsCfg.MySQLWatchPollInterval)
	}
}

func TestLoadConfigAcceptsEtcdTLSWithoutClientCertificate(t *testing.T) {
	path := writeConfigFile(t, `
datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    tls: true
    ca_file: /tmp/ca.crt
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.DataStore.Etcd.TLS {
		t.Fatal("DataStore.Etcd.TLS = false, want true")
	}
	if cfg.DataStore.Etcd.CertFile != "" || cfg.DataStore.Etcd.KeyFile != "" {
		t.Fatalf("etcd client certificate files = %q/%q, want optional client cert unset", cfg.DataStore.Etcd.CertFile, cfg.DataStore.Etcd.KeyFile)
	}
	if cfg.DataStore.Etcd.CAFile != "/tmp/ca.crt" {
		t.Fatalf("DataStore.Etcd.CAFile = %q, want /tmp/ca.crt", cfg.DataStore.Etcd.CAFile)
	}
}

func TestLoadConfigRejectsInvalidServerLimits(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "invalid port",
			yaml:    "server:\n  port: 70000\n",
			wantErr: "server.port",
		},
		{
			name:    "short api key",
			yaml:    "server:\n  api_key: short\n",
			wantErr: "server.api_key",
		},
		{
			name:    "api key with whitespace",
			yaml:    "server:\n  api_key: controller secret value\n",
			wantErr: "server.api_key",
		},
		{
			name:    "negative body limit",
			yaml:    "server:\n  max_body_bytes: -1\n",
			wantErr: "server.max_body_bytes",
		},
		{
			name:    "zero read timeout",
			yaml:    "server:\n  read_timeout: -1s\n",
			wantErr: "server.read_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig error = %v, want %s validation error", err, tt.wantErr)
			}
		})
	}
}

func minimalEtcdConfig() string {
	return `
datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
`
}

func quoteYAMLString(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "controller.yaml")
	return writeFile(t, filepath.Dir(path), filepath.Base(path), contents)
}

func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
