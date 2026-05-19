package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// TCPProber implements TCP health checks
type TCPProber struct {
	logger *logrus.Logger

	// Configuration
	port    int
	send    string // Optional data to send
	expect  string // Optional expected response substring
	timeout time.Duration
}

// NewTCPProber creates a new TCP prober
func NewTCPProber(hc *models.HealthCheck, logger *logrus.Logger) (*TCPProber, error) {
	if hc == nil {
		return nil, fmt.Errorf("health check configuration is nil")
	}
	if err := models.ValidateHealthCheckConfig(hc.Type, hc.Config); err != nil {
		return nil, fmt.Errorf("invalid TCP health check config: %w", err)
	}

	prober := &TCPProber{
		logger:  logger,
		timeout: time.Duration(hc.TimeoutSec) * time.Second,
	}

	// Parse configuration from HCConfig map
	if err := prober.parseConfig(hc.Config); err != nil {
		return nil, fmt.Errorf("failed to parse TCP prober config: %w", err)
	}

	return prober, nil
}

// parseConfig parses the HCConfig map to extract TCP-specific configuration
func (p *TCPProber) parseConfig(config models.HCConfig) error {
	// Port (required)
	port, ok := config["port"]
	if !ok {
		return fmt.Errorf("port is required in TCP health check config")
	}
	parsedPort, ok := healthCheckConfigInt(port)
	if !ok {
		return fmt.Errorf("port must be an integer, got %T", port)
	}
	p.port = parsedPort

	// Send (optional)
	if send, ok := config["send"].(string); ok {
		p.send = send
	}

	// Expect (optional)
	if expect, ok := config["expect"].(string); ok {
		p.expect = expect
	}

	return nil
}

// Probe performs a TCP health check
func (p *TCPProber) Probe(ctx context.Context, target string) ProbeResult {
	startTime := time.Now()
	if err := validateProbeTarget(target); err != nil {
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     err,
			Timestamp: startTime,
		}
	}

	address := tcpProbeAddress(target, p.port)

	// Create dialer with timeout
	dialer := &net.Dialer{
		Timeout: p.timeout,
	}

	// Connect to target
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     fmt.Errorf("TCP connection failed: %w", err),
			Timestamp: startTime,
		}
	}
	defer func() { _ = conn.Close() }()

	// Set deadline for the entire operation.
	deadline := startTime.Add(p.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     fmt.Errorf("failed to set deadline: %w", err),
			Timestamp: startTime,
		}
	}
	stopCancelWatcher := bindTCPConnCancelToContext(ctx, conn)
	defer stopCancelWatcher()

	// If no send/expect, just successful connection is enough
	if p.send == "" && p.expect == "" {
		latency := time.Since(startTime)
		return ProbeResult{
			Success:   true,
			Latency:   latency,
			Timestamp: startTime,
		}
	}

	// Send data if configured
	if p.send != "" {
		_, err := conn.Write([]byte(p.send))
		if err != nil {
			err = tcpProbeContextError(ctx, err)
			return ProbeResult{
				Success:   false,
				Latency:   time.Since(startTime),
				Error:     fmt.Errorf("TCP send failed: %w", err),
				Timestamp: startTime,
			}
		}
	}

	// Read and check response if expected string is configured
	if p.expect != "" {
		// Read response (up to 4KB)
		buffer := make([]byte, 4096)
		n, err := conn.Read(buffer)
		if err != nil {
			err = tcpProbeContextError(ctx, err)
			return ProbeResult{
				Success:   false,
				Latency:   time.Since(startTime),
				Error:     fmt.Errorf("TCP read failed: %w", err),
				Timestamp: startTime,
			}
		}

		response := string(buffer[:n])

		// Check if response contains expected substring
		if !strings.Contains(response, p.expect) {
			return ProbeResult{
				Success:   false,
				Latency:   time.Since(startTime),
				Error:     fmt.Errorf("TCP response does not contain expected string: %q", p.expect),
				Timestamp: startTime,
			}
		}
	}

	latency := time.Since(startTime)
	return ProbeResult{
		Success:   true,
		Latency:   latency,
		Timestamp: startTime,
	}
}

// Close cleans up resources
func (p *TCPProber) Close() error {
	// No persistent resources to clean up
	return nil
}

func tcpProbeContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
	}
	return err
}

func bindTCPConnCancelToContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()

	return func() { close(done) }
}
