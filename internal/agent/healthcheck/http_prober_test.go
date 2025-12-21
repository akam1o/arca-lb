package healthcheck

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/test/bufconn"
)

const httpBufSize = 1024 * 1024

func startBufferedHTTPServer(t *testing.T, handler http.Handler) *bufconn.Listener {
	lis := bufconn.Listen(httpBufSize)
	server := &http.Server{
		Handler: handler,
	}

	go func() {
		_ = server.Serve(lis)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		lis.Close()
	})

	return lis
}

func TestNewHTTPProber(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name      string
		hc        *models.HealthCheck
		useHTTPS  bool
		wantError bool
	}{
		{
			name: "valid HTTP config",
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				Config: models.HCConfig{
					"port": 80,
					"path": "/health",
				},
			},
			useHTTPS:  false,
			wantError: false,
		},
		{
			name: "valid HTTPS config",
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				Config: models.HCConfig{
					"port": 443,
					"path": "/health",
				},
			},
			useHTTPS:  true,
			wantError: false,
		},
		{
			name:      "nil config",
			hc:        nil,
			wantError: true,
		},
		{
			name: "missing port",
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				Config: models.HCConfig{
					"path": "/health",
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober, err := NewHTTPProber(tt.hc, tt.useHTTPS, logger)
			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, prober)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, prober)
				if prober != nil {
					defer prober.Close()
				}
			}
		})
	}
}

func TestHTTPProber_Probe(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name            string
		serverHandler   http.HandlerFunc
		hc              *models.HealthCheck
		expectedSuccess bool
		expectedCode    int
	}{
		{
			name: "success - 200 OK",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("OK"))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			},
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				Config: models.HCConfig{
					"path":           "/health",
					"expected_codes": []interface{}{200},
				},
			},
			expectedSuccess: true,
			expectedCode:    200,
		},
		{
			name: "failure - 500 error",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/error" {
					w.WriteHeader(http.StatusInternalServerError)
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			},
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				Config: models.HCConfig{
					"path":           "/error",
					"expected_codes": []interface{}{200},
				},
			},
			expectedSuccess: false,
			expectedCode:    500,
		},
		{
			name:          "failure - connection refused",
			serverHandler: nil, // No server
			hc: &models.HealthCheck{
				Type:        models.HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  1,
				Config: models.HCConfig{
					"port": 9999,
					"path": "/health",
				},
			},
			expectedSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var listener *bufconn.Listener
			var target string
			var port int

			if tt.serverHandler != nil {
				listener = startBufferedHTTPServer(t, tt.serverHandler)
				port = 8080
				target = "buffered-server"

				// Update HC config with actual port
				tt.hc.Config["port"] = port
			} else {
				// Connection refused test
				target = "127.0.0.1"
				port = 9999
			}

			prober, err := NewHTTPProber(tt.hc, false, logger)
			require.NoError(t, err)

			if listener != nil {
				prober.client.Transport = &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return listener.DialContext(ctx)
					},
				}
			}
			defer prober.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result := prober.Probe(ctx, target)

			if tt.expectedSuccess {
				assert.True(t, result.Success, "Expected success but got failure: %v", result.Error)
				assert.NoError(t, result.Error)
				if tt.expectedCode > 0 {
					assert.Equal(t, tt.expectedCode, result.StatusCode)
				}
			} else {
				assert.False(t, result.Success, "Expected failure but got success")
			}
		})
	}
}

func TestHTTPProber_Probe_WithCustomHeaders(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create test server that checks headers
	listener := startBufferedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Header") == "test-value" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))

	hc := &models.HealthCheck{
		Type:        models.HCTypeHTTP,
		IntervalSec: 10,
		TimeoutSec:  5,
		Config: models.HCConfig{
			"port": 8081,
			"path": "/",
			"headers": map[string]interface{}{
				"X-Custom-Header": "test-value",
			},
			"expected_codes": []interface{}{200},
		},
	}

	prober, err := NewHTTPProber(hc, false, logger)
	require.NoError(t, err)

	prober.client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return listener.DialContext(ctx)
		},
	}
	defer prober.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := prober.Probe(ctx, "buffered-server")

	assert.True(t, result.Success, "Expected success with custom header")
	assert.Equal(t, 200, result.StatusCode)
	assert.NoError(t, result.Error)
}
