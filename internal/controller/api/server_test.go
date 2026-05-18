package api

import (
	"crypto/tls"
	"testing"
)

func TestNewHTTPServerSetsTLS12MinimumWhenEnabled(t *testing.T) {
	server, _ := setupTestServer()
	server.config.Server.TLS = true

	httpServer := server.newHTTPServer("127.0.0.1:0")
	if httpServer.TLSConfig == nil {
		t.Fatal("TLSConfig = nil, want TLS configuration")
	}
	if httpServer.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", httpServer.TLSConfig.MinVersion)
	}
}

func TestNewHTTPServerLeavesTLSConfigUnsetWhenDisabled(t *testing.T) {
	server, _ := setupTestServer()
	server.config.Server.TLS = false

	httpServer := server.newHTTPServer("127.0.0.1:0")
	if httpServer.TLSConfig != nil {
		t.Fatalf("TLSConfig = %#v, want nil", httpServer.TLSConfig)
	}
}
