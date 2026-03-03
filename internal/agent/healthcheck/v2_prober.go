package healthcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

// V2ProbeResult holds the outcome of a single probe execution.
type V2ProbeResult struct {
	Success    bool
	Latency    time.Duration
	StatusCode int
	Error      error
	Timestamp  time.Time
}

// V2Prober executes health probes against a target address.
type V2Prober interface {
	Probe(ctx context.Context, target string) V2ProbeResult
	Close() error
}

// newProberFromSpec creates a V2Prober from a CRD HealthCheckSpec.
func newProberFromSpec(spec *v1alpha1.HealthCheckSpec) (V2Prober, error) {
	switch spec.Type {
	case v1alpha1.HCTypeHTTP:
		return newHTTPProberFromSpec(spec.HTTP, false)
	case v1alpha1.HCTypeHTTPS:
		return newHTTPProberFromSpec(spec.HTTP, true)
	case v1alpha1.HCTypeTCP:
		return newTCPProberFromSpec(spec.TCP)
	case v1alpha1.HCTypePing:
		return &pingProber{}, nil
	default:
		return nil, fmt.Errorf("unsupported health check type: %s", spec.Type)
	}
}

// --- HTTP/HTTPS prober ---

type httpProber struct {
	client        *http.Client
	port          int
	path          string
	method        string
	host          string
	headers       map[string]string
	expectedCodes map[int]bool
	useTLS        bool
}

func newHTTPProberFromSpec(cfg *v1alpha1.HTTPHealthCheck, useTLS bool) (*httpProber, error) {
	if cfg == nil {
		return nil, fmt.Errorf("HTTP health check config is required")
	}

	transport := &http.Transport{
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	}
	if useTLS && cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user-configured
	}

	method := cfg.Method
	if method == "" {
		method = "GET"
	}
	path := cfg.Path
	if path == "" {
		path = "/"
	}

	expectedCodes := make(map[int]bool)
	if len(cfg.ExpectedCodes) == 0 {
		for c := 200; c < 300; c++ {
			expectedCodes[c] = true
		}
	} else {
		for _, c := range cfg.ExpectedCodes {
			expectedCodes[c] = true
		}
	}

	return &httpProber{
		client:        &http.Client{Transport: transport},
		port:          cfg.Port,
		path:          path,
		method:        method,
		host:          cfg.Host,
		headers:       cfg.Headers,
		expectedCodes: expectedCodes,
		useTLS:        useTLS,
	}, nil
}

func (p *httpProber) Probe(ctx context.Context, target string) V2ProbeResult {
	start := time.Now()

	scheme := "http"
	if p.useTLS {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:%d%s", scheme, target, p.port, p.path)

	req, err := http.NewRequestWithContext(ctx, p.method, url, nil)
	if err != nil {
		return V2ProbeResult{Error: err, Timestamp: start}
	}

	if p.host != "" {
		req.Host = p.host
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	resp.Body.Close()

	success := p.expectedCodes[resp.StatusCode]
	return V2ProbeResult{
		Success:    success,
		Latency:    time.Since(start),
		StatusCode: resp.StatusCode,
		Timestamp:  start,
	}
}

func (p *httpProber) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

// --- TCP prober ---

type tcpProber struct {
	port             int
	send             string
	expectedResponse string
}

func newTCPProberFromSpec(cfg *v1alpha1.TCPHealthCheck) (*tcpProber, error) {
	if cfg == nil {
		return nil, fmt.Errorf("TCP health check config is required")
	}
	return &tcpProber{
		port:             cfg.Port,
		send:             cfg.Send,
		expectedResponse: cfg.ExpectedResponse,
	}, nil
}

func (p *tcpProber) Probe(ctx context.Context, target string) V2ProbeResult {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", target, p.port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	defer conn.Close()

	if p.send != "" {
		if _, err := conn.Write([]byte(p.send)); err != nil {
			return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
		}
	}

	if p.expectedResponse != "" {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
		}
		if !strings.Contains(string(buf[:n]), p.expectedResponse) {
			return V2ProbeResult{
				Error:   fmt.Errorf("response did not contain %q", p.expectedResponse),
				Latency: time.Since(start),
				Timestamp: start,
			}
		}
	}

	return V2ProbeResult{Success: true, Latency: time.Since(start), Timestamp: start}
}

func (p *tcpProber) Close() error { return nil }

// --- Ping prober ---

type pingProber struct{}

func (p *pingProber) Probe(ctx context.Context, target string) V2ProbeResult {
	start := time.Now()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", target)
	case "darwin":
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-t", "2", target)
	default:
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", target)
	}

	if err := cmd.Run(); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}

	return V2ProbeResult{Success: true, Latency: time.Since(start), Timestamp: start}
}

func (p *pingProber) Close() error { return nil }
