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
