package healthcheck

import (
	"context"
	"crypto/tls"
	"fmt"
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
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: prober.tlsSkipVerify,
			},
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
	switch v := port.(type) {
	case int:
		p.port = v
	case float64:
		p.port = int(v)
	default:
		return fmt.Errorf("port must be an integer, got %T", port)
	}

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
	if expectedCodes, ok := config["expected_codes"].([]interface{}); ok {
		for _, code := range expectedCodes {
			switch v := code.(type) {
			case int:
				p.expectedCodes[v] = true
			case float64:
				p.expectedCodes[int(v)] = true
			default:
				return fmt.Errorf("expected_codes must be integers, got %T", code)
			}
		}
	} else {
		// Default to 200
		p.expectedCodes[200] = true
	}

	// Headers (optional)
	p.headers = make(map[string]string)
	if headers, ok := config["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			if strValue, ok := value.(string); ok {
				p.headers[key] = strValue
			}
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

// Probe performs an HTTP/HTTPS health check
func (p *HTTPProber) Probe(ctx context.Context, target string) ProbeResult {
	startTime := time.Now()

	// Build URL
	scheme := "http"
	if p.useHTTPS {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:%d%s", scheme, target, p.port, p.path)

	// Create request
	req, err := http.NewRequestWithContext(ctx, p.method, url, nil)
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
	defer resp.Body.Close()

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
