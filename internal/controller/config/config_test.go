package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigRequiresGRPCTLSFiles(t *testing.T) {
	path := writeConfigFile(t, `
grpc:
  tls: true
`)

	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "grpc.cert_file is required") {
		t.Fatalf("LoadConfig error = %v, want grpc.cert_file validation error", err)
	}
}

func TestLoadConfigRequiresGRPCClientCAWhenClientCertRequired(t *testing.T) {
	path := writeConfigFile(t, `
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

func TestLoadConfigAcceptsGRPCTLSFiles(t *testing.T) {
	path := writeConfigFile(t, `
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

func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "controller.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
