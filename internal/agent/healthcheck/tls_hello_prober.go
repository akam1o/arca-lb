package healthcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// TLSHelloProber verifies that the backend can complete a TLS handshake.
type TLSHelloProber struct {
	logger  *logrus.Logger
	port    int
	timeout time.Duration
}

// NewTLSHelloProber creates a new TLS hello prober.
func NewTLSHelloProber(hc *models.HealthCheck, logger *logrus.Logger) (*TLSHelloProber, error) {
	if hc == nil {
		return nil, fmt.Errorf("health check configuration is nil")
	}

	port, err := parseTLSHelloPort(hc.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TLS hello prober config: %w", err)
	}

	return &TLSHelloProber{
		logger:  logger,
		port:    port,
		timeout: time.Duration(hc.TimeoutSec) * time.Second,
	}, nil
}

func parseTLSHelloPort(config models.HCConfig) (int, error) {
	portRaw, ok := config["port"]
	if !ok {
		return 0, fmt.Errorf("port is required in TLS hello health check config")
	}

	var port int
	switch v := portRaw.(type) {
	case int:
		port = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
			return 0, fmt.Errorf("port must be an integer, got %v", v)
		}
		port = int(v)
	default:
		return 0, fmt.Errorf("port must be an integer, got %T", portRaw)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}

	return port, nil
}

// Probe performs a TLS hello health check.
func (p *TLSHelloProber) Probe(ctx context.Context, target string) ProbeResult {
	startTime := time.Now()
	probeCtx, cancel := contextWithDefaultTimeout(ctx, p.timeout)
	defer cancel()

	dialer := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config:    newHealthCheckTLSConfig(true),
	}
	conn, err := dialer.DialContext(probeCtx, "tcp", tcpProbeAddress(target, p.port))
	if err != nil {
		if probeCtx.Err() != nil {
			err = probeCtx.Err()
		}
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     fmt.Errorf("TLS hello failed: %w", err),
			Timestamp: startTime,
		}
	}
	defer func() { _ = conn.Close() }()

	return ProbeResult{
		Success:   true,
		Latency:   time.Since(startTime),
		Timestamp: startTime,
	}
}

// Close cleans up resources held by the prober.
func (p *TLSHelloProber) Close() error {
	return nil
}
