package healthcheck

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// HTTPProber implements HTTP/HTTPS health checks
type HTTPProber struct {
	logger *logrus.Logger

	// Configuration
	useHTTPS      bool
	port          int
	path          string
	method        string
	expectedCodes map[int]bool
	headers       map[string]string
	tlsSkipVerify bool
	hostHeader    string
	timeout       time.Duration

	// HTTP client
	client *http.Client
}

// NewHTTPProber creates a new HTTP/HTTPS prober
func NewHTTPProber(hc *models.HealthCheck, useHTTPS bool, logger *logrus.Logger) (*HTTPProber, error) {
	if hc == nil {
		return nil, fmt.Errorf("health check configuration is nil")
	}
	if err := models.ValidateHealthCheckConfig(hc.Type, hc.Config); err != nil {
		return nil, fmt.Errorf("invalid HTTP health check config: %w", err)
	}

	prober := &HTTPProber{
		logger:   logger,
		useHTTPS: useHTTPS,
		timeout:  time.Duration(hc.TimeoutSec) * time.Second,
	}

	// Parse configuration from HCConfig map
	if err := prober.parseConfig(hc.Config); err != nil {
		return nil, fmt.Errorf("failed to parse HTTP prober config: %w", err)
	}

	// Create HTTP client
	prober.client = &http.Client{
		Timeout: prober.timeout,
		Transport: &http.Transport{
			TLSClientConfig: newHealthCheckTLSConfig(prober.tlsSkipVerify),
			DialContext: (&net.Dialer{
				Timeout:   prober.timeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: prober.timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects
			return http.ErrUseLastResponse
		},
	}

	return prober, nil
}

// parseConfig parses the HCConfig map to extract HTTP-specific configuration
func (p *HTTPProber) parseConfig(config models.HCConfig) error {
	// Port (required)
	port, ok := config["port"]
	if !ok {
		return fmt.Errorf("port is required in HTTP health check config")
	}
	parsedPort, ok := healthCheckConfigInt(port)
	if !ok {
		return fmt.Errorf("port must be an integer, got %T", port)
	}
	p.port = parsedPort

	// Path (optional, default: "/")
	if path, ok := config["path"].(string); ok {
		p.path = path
	} else {
		p.path = "/"
	}

	// Method (optional, default: "GET")
	if method, ok := config["method"].(string); ok {
		p.method = strings.ToUpper(method)
	} else {
		p.method = "GET"
	}

	// Expected status codes (optional, default: [200])
	p.expectedCodes = make(map[int]bool)
	if expectedCodes, ok := config["expected_codes"]; ok && expectedCodes != nil {
		codes, err := parseHTTPExpectedCodes(expectedCodes)
		if err != nil {
			return err
		}
		p.expectedCodes = codes
	} else {
		// Default to 200
		p.expectedCodes[200] = true
	}

	// Headers (optional)
	p.headers = make(map[string]string)
	switch headers := config["headers"].(type) {
	case map[string]interface{}:
		for key, value := range headers {
			if value == nil {
				continue
			}
			if strValue, ok := value.(string); ok {
				p.headers[key] = strValue
			}
		}
	case map[string]string:
		for key, value := range headers {
			p.headers[key] = value
		}
	}

	// TLS skip verify (optional, default: false)
	if tlsSkipVerify, ok := config["tls_skip_verify"].(bool); ok {
		p.tlsSkipVerify = tlsSkipVerify
	}

	// Host header (optional)
	if hostHeader, ok := config["host_header"].(string); ok {
		p.hostHeader = hostHeader
	}

	return nil
}

func parseHTTPExpectedCodes(raw any) (map[int]bool, error) {
	codes := make(map[int]bool)

	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			code, ok := healthCheckConfigInt(value)
			if !ok {
				return nil, fmt.Errorf("expected_codes must be integers, got %T", value)
			}
			codes[code] = true
		}
	case []int:
		for _, value := range values {
			codes[value] = true
		}
	case []float64:
		for _, value := range values {
			code, ok := healthCheckConfigInt(value)
			if !ok {
				return nil, fmt.Errorf("expected_codes must be integers, got %T", value)
			}
			codes[code] = true
		}
	default:
		return nil, fmt.Errorf("expected_codes must be an array of integers")
	}

	return codes, nil
}

func healthCheckConfigInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}

// Probe performs an HTTP/HTTPS health check
func (p *HTTPProber) Probe(ctx context.Context, target string) ProbeResult {
	startTime := time.Now()
	if err := validateProbeTarget(target); err != nil {
		return ProbeResult{
			Success:   false,
			Latency:   time.Since(startTime),
			Error:     err,
			Timestamp: startTime,
		}
	}

	scheme := "http"
	if p.useHTTPS {
		scheme = "https"
	}
	probeURL, err := buildHTTPProbeURL(scheme, target, p.port, p.path)
	if err != nil {
		return ProbeResult{
			Success:    false,
			Latency:    time.Since(startTime),
			StatusCode: 0,
			Error:      fmt.Errorf("failed to build request URL: %w", err),
			Timestamp:  startTime,
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, p.method, probeURL, nil)
	if err != nil {
		return ProbeResult{
			Success:    false,
			Latency:    time.Since(startTime),
			StatusCode: 0,
			Error:      fmt.Errorf("failed to create request: %w", err),
			Timestamp:  startTime,
		}
	}

	// Set custom Host header if specified
	if p.hostHeader != "" {
		req.Host = p.hostHeader
	}

	// Set custom headers
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "arca-lb-healthcheck/1.0")

	// Execute request
	resp, err := p.client.Do(req)
	latency := time.Since(startTime)

	if err != nil {
		return ProbeResult{
			Success:    false,
			Latency:    latency,
			StatusCode: 0,
			Error:      fmt.Errorf("HTTP request failed: %w", err),
			Timestamp:  startTime,
		}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logger.WithError(err).Debug("failed to close response body")
		}
	}()

	// Check if status code is expected
	success := p.expectedCodes[resp.StatusCode]

	result := ProbeResult{
		Success:    success,
		Latency:    latency,
		StatusCode: resp.StatusCode,
		Timestamp:  startTime,
	}

	if !success {
		result.Error = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return result
}

// Close cleans up resources
func (p *HTTPProber) Close() error {
	if p.client != nil {
		p.client.CloseIdleConnections()
	}
	return nil
}
