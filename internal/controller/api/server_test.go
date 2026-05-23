package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	logrustest "github.com/sirupsen/logrus/hooks/test"
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

func TestNewServerDoesNotTrustForwardedClientIPHeaders(t *testing.T) {
	server, _ := setupTestServer()
	server.router.GET("/client-ip-test", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	req := httptest.NewRequest(http.MethodGet, "/client-ip-test", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("X-Real-IP", "203.0.113.20")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "192.0.2.10" {
		t.Fatalf("client IP = %q, want remote address IP", got)
	}
}

func TestLoggingMiddlewarePropagatesRequestID(t *testing.T) {
	server, _ := setupTestServer()
	logger, hook := logrustest.NewNullLogger()
	server.logger = logger
	server.router.GET("/request-id-test", func(c *gin.Context) {
		requestID, ok := c.Get("request_id")
		if !ok {
			t.Fatal("request_id was not set in gin context")
		}
		c.String(http.StatusOK, requestID.(string))
	})

	req := httptest.NewRequest(http.MethodGet, "/request-id-test", nil)
	req.Header.Set(requestIDHeader, "req-123")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get(requestIDHeader); got != "req-123" {
		t.Fatalf("%s response header = %q, want req-123", requestIDHeader, got)
	}
	if got := w.Body.String(); got != "req-123" {
		t.Fatalf("request id context value = %q, want req-123", got)
	}
	entry := hook.LastEntry()
	if entry == nil {
		t.Fatal("log entry was not captured")
	}
	if got := entry.Data["request_id"]; got != "req-123" {
		t.Fatalf("logged request_id = %v, want req-123", got)
	}
}

func TestLoggingMiddlewareGeneratesRequestID(t *testing.T) {
	server, _ := setupTestServer()
	logger, _ := logrustest.NewNullLogger()
	server.logger = logger
	server.router.GET("/generated-request-id-test", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/generated-request-id-test", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get(requestIDHeader); got == "" {
		t.Fatalf("%s response header is empty, want generated request id", requestIDHeader)
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
