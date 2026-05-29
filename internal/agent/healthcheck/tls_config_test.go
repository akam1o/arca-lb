package healthcheck

import (
	"crypto/tls"
	"testing"
)

func TestNewHealthCheckTLSConfigUsesTLS12Minimum(t *testing.T) {
	cfg := newHealthCheckTLSConfig(false)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = true, want false")
	}
}

func TestNewHealthCheckTLSConfigHonorsSkipVerify(t *testing.T) {
	cfg := newHealthCheckTLSConfig(true)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want TLS 1.2", cfg.MinVersion)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true")
	}
}
