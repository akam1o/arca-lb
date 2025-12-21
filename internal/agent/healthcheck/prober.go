package healthcheck

import (
	"context"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// ProbeResult represents the result of a single health check probe
type ProbeResult struct {
	// Success indicates if the probe succeeded
	Success bool

	// Latency is the time taken for the probe
	Latency time.Duration

	// StatusCode is the HTTP status code (for HTTP/HTTPS probes)
	StatusCode int

	// Error contains the error if the probe failed
	Error error

	// Timestamp is when the probe was executed
	Timestamp time.Time
}

// Prober is the interface for health check probers
type Prober interface {
	// Probe performs a health check on the target
	// target is typically the backend IP address
	Probe(ctx context.Context, target string) ProbeResult

	// Close cleans up any resources held by the prober
	Close() error
}

// ProberFactory creates a Prober instance based on the health check configuration
func NewProber(hc *models.HealthCheck, logger *logrus.Logger) (Prober, error) {
	if hc == nil {
		return nil, fmt.Errorf("health check configuration is nil")
	}

	switch hc.Type {
	case models.HCTypeHTTP:
		return NewHTTPProber(hc, false, logger)
	case models.HCTypeHTTPS:
		return NewHTTPProber(hc, true, logger)
	case models.HCTypeTCP:
		return NewTCPProber(hc, logger)
	case models.HCTypePing:
		return NewPingProber(hc, logger)
	default:
		return nil, fmt.Errorf("unsupported health check type: %s", hc.Type)
	}
}
