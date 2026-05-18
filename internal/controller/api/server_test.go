package api

import (
	"context"
	"crypto/tls"
	"strings"
	"testing"
	"time"
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

func TestStartRejectsAPIKeyWithoutTLS(t *testing.T) {
	server, _ := setupTestServer()
	server.config.Server.Host = "127.0.0.1"
	server.config.Server.Port = 0
	server.config.Server.APIKey = "controller-rest-secret"
	server.config.Server.TLS = false

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "server.tls must be enabled when server.api_key is set") {
			t.Fatalf("Start error = %v, want server.api_key TLS validation error", err)
		}
	case <-time.After(500 * time.Millisecond):
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		t.Fatal("Start did not reject API key without TLS")
	}
}
