package healthcheck

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// PingProber implements ICMP ping health checks using external ping binary
type PingProber struct {
	logger  *logrus.Logger
	timeout time.Duration
}

// NewPingProber creates a new Ping prober
func NewPingProber(hc *models.HealthCheck, logger *logrus.Logger) (*PingProber, error) {
	if hc == nil {
		return nil, fmt.Errorf("health check configuration is nil")
	}

	prober := &PingProber{
		logger:  logger,
		timeout: time.Duration(hc.TimeoutSec) * time.Second,
	}

	return prober, nil
}

// Probe performs an ICMP ping health check
func (p *PingProber) Probe(ctx context.Context, target string) ProbeResult {
	startTime := time.Now()
	if err := validatePingTarget(target); err != nil {
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     err,
			Timestamp: startTime,
		}
	}

	probeCtx, cancel := contextWithDefaultTimeout(ctx, p.timeout)
	defer cancel()

	// Build ping command based on OS
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		// Linux: ping -c 1 -W <timeout_secs> <target>
		timeoutSecs := fmt.Sprintf("%d", int(p.timeout.Seconds()))
		cmd = exec.CommandContext(probeCtx, "ping", "-c", "1", "-W", timeoutSecs, target)
	case "darwin":
		// macOS: ping -c 1 -W <timeout_ms> <target>
		timeoutMs := fmt.Sprintf("%d", p.timeout.Milliseconds())
		cmd = exec.CommandContext(probeCtx, "ping", "-c", "1", "-W", timeoutMs, target)
	case "windows":
		// Windows: ping -n 1 -w <timeout_ms> <target>
		timeoutMs := fmt.Sprintf("%d", p.timeout.Milliseconds())
		cmd = exec.CommandContext(probeCtx, "ping", "-n", "1", "-w", timeoutMs, target)
	default:
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     fmt.Errorf("ping not supported on OS: %s", runtime.GOOS),
			Timestamp: startTime,
		}
	}

	// Execute ping command
	output, err := cmd.CombinedOutput()
	latency := time.Since(startTime)

	if err != nil {
		if probeCtx.Err() != nil {
			err = probeCtx.Err()
		}
		// Ping failed
		return ProbeResult{
			Success:   false,
			Latency:   latency,
			Error:     fmt.Errorf("ping failed: %w (output: %s)", err, strings.TrimSpace(string(output))),
			Timestamp: startTime,
		}
	}

	// Ping succeeded
	return ProbeResult{
		Success:   true,
		Latency:   latency,
		Timestamp: startTime,
	}
}

// Close cleans up resources
func (p *PingProber) Close() error {
	// No persistent resources to clean up
	return nil
}
