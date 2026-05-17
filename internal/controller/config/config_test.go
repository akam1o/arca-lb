package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			name: "etcd tls missing cert",
			yaml: `datastore:
  type: etcd
  etcd:
    endpoints: ["127.0.0.1:2379"]
    tls: true
`,
			wantErr: "datastore.etcd.cert_file",
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
    database: arcalb
`,
			wantErr: "datastore.mysql.port",
		},
		{
			name: "mysql missing database",
			yaml: `datastore:
  type: mysql
  mysql:
    host: 127.0.0.1
    port: 3306
`,
			wantErr: "datastore.mysql.database",
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

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "controller.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
