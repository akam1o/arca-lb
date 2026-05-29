package healthcheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProberTLSHello(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	prober, err := NewProber(&models.HealthCheck{
		Type:       models.HCTypeTLSHello,
		TimeoutSec: 1,
		Config: models.HCConfig{
			"port": 443,
		},
	}, logger)
	if err != nil {
		t.Fatalf("NewProber: %v", err)
	}
	if _, ok := prober.(*TLSHelloProber); !ok {
		t.Fatalf("prober type = %T, want *TLSHelloProber", prober)
	}
}

func TestNewTLSHelloProberAcceptsValidatedIntegerPortShapes(t *testing.T) {
	tests := []struct {
		name string
		port any
		want int
	}{
		{name: "int32", port: int32(8443), want: 8443},
		{name: "int64", port: int64(8444), want: 8444},
		{name: "float64 integer", port: float64(8445), want: 8445},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober, err := NewTLSHelloProber(&models.HealthCheck{
				Type:       models.HCTypeTLSHello,
				TimeoutSec: 1,
				Config: models.HCConfig{
					"port": tt.port,
				},
			}, logrus.New())

			require.NoError(t, err)
			assert.Equal(t, tt.want, prober.port)
		})
	}
}

func TestNewTLSHelloProberRejectsInvalidRuntimeConfig(t *testing.T) {
	tests := []struct {
		name string
		port any
	}{
		{name: "fractional port", port: 8443.5},
		{name: "out of range port", port: 70000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober, err := NewTLSHelloProber(&models.HealthCheck{
				Type:       models.HCTypeTLSHello,
				TimeoutSec: 1,
				Config: models.HCConfig{
					"port": tt.port,
				},
			}, logrus.New())

			require.Error(t, err)
			assert.Nil(t, prober)
			assert.Contains(t, err.Error(), "invalid TLS hello health check config")
		})
	}
}

func TestTLSHelloProberSucceedsWithTLSServerLegacy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	host, portStr, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("net.SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("strconv.Atoi: %v", err)
	}

	prober := &TLSHelloProber{
		port:    port,
		timeout: time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := prober.Probe(ctx, host)
	if !result.Success {
		t.Fatalf("TLS hello probe failed: %v", result.Error)
	}
}
