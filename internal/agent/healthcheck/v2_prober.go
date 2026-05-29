package healthcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
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
	case v1alpha1.HCTypeTLSHello:
		return newTLSHelloProberFromSpec(spec.TCP)
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
	if useTLS {
		transport.TLSClientConfig = newHealthCheckTLSConfig(cfg.SkipTLSVerify)
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
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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
	probeURL, err := buildHTTPProbeURL(scheme, target, p.port, p.path)
	if err != nil {
		return V2ProbeResult{Error: err, Timestamp: start}
	}

	req, err := http.NewRequestWithContext(ctx, p.method, probeURL, nil)
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
	if err := resp.Body.Close(); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}

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

func buildHTTPProbeURL(scheme, target string, port int, probePath string) (string, error) {
	if err := validateProbeTarget(target); err != nil {
		return "", err
	}
	if probePath == "" {
		probePath = "/"
	}
	if !strings.HasPrefix(probePath, "/") {
		probePath = "/" + probePath
	}

	parsedPath, err := url.Parse(probePath)
	if err != nil {
		return "", fmt.Errorf("invalid HTTP probe path %q: %w", probePath, err)
	}
	if parsedPath.IsAbs() || parsedPath.Host != "" {
		return "", fmt.Errorf("HTTP probe path must be relative: %q", probePath)
	}

	u := url.URL{
		Scheme:   scheme,
		Host:     net.JoinHostPort(target, strconv.Itoa(port)),
		Path:     parsedPath.Path,
		RawQuery: parsedPath.RawQuery,
	}
	return u.String(), nil
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
	if err := validateProbeTarget(target); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	addr := tcpProbeAddress(target, p.port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	defer func() { _ = conn.Close() }()

	stopCancelWatcher, err := bindConnToContext(ctx, conn)
	if err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	defer stopCancelWatcher()

	if p.send != "" {
		if _, err := conn.Write([]byte(p.send)); err != nil {
			err = tcpProbeContextError(ctx, err)
			return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
		}
	}

	if p.expectedResponse != "" {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			err = tcpProbeContextError(ctx, err)
			return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
		}
		if !strings.Contains(string(buf[:n]), p.expectedResponse) {
			return V2ProbeResult{
				Error:     fmt.Errorf("response did not contain %q", p.expectedResponse),
				Latency:   time.Since(start),
				Timestamp: start,
			}
		}
	}

	return V2ProbeResult{Success: true, Latency: time.Since(start), Timestamp: start}
}

func (p *tcpProber) Close() error { return nil }

func tcpProbeAddress(target string, port int) string {
	return net.JoinHostPort(target, strconv.Itoa(port))
}

func bindConnToContext(ctx context.Context, conn net.Conn) (func(), error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("failed to set TCP probe deadline: %w", err)
		}
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()

	return func() { close(done) }, nil
}

// --- TLS hello prober ---

type tlsHelloProber struct {
	port int
}

func newTLSHelloProberFromSpec(cfg *v1alpha1.TCPHealthCheck) (*tlsHelloProber, error) {
	if cfg == nil {
		return nil, fmt.Errorf("TCP health check config is required for TLS hello")
	}
	return &tlsHelloProber{port: cfg.Port}, nil
}

func (p *tlsHelloProber) Probe(ctx context.Context, target string) V2ProbeResult {
	start := time.Now()
	if err := validateProbeTarget(target); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	addr := tcpProbeAddress(target, p.port)

	dialer := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config:    newHealthCheckTLSConfig(true),
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}
	defer func() { _ = conn.Close() }()

	return V2ProbeResult{Success: true, Latency: time.Since(start), Timestamp: start}
}

func (p *tlsHelloProber) Close() error { return nil }

// --- Ping prober ---

type pingProber struct{}

const defaultPingTimeout = 2 * time.Second

func (p *pingProber) Probe(ctx context.Context, target string) V2ProbeResult {
	start := time.Now()
	if err := validateProbeTarget(target); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}

	pingCtx, cancel := contextWithDefaultTimeout(ctx, defaultPingTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-W", pingTimeoutSeconds(pingCtx, defaultPingTimeout), target)
	case "darwin":
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", target)
	default:
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", target)
	}

	if err := cmd.Run(); err != nil {
		return V2ProbeResult{Error: err, Latency: time.Since(start), Timestamp: start}
	}

	return V2ProbeResult{Success: true, Latency: time.Since(start), Timestamp: start}
}

func (p *pingProber) Close() error { return nil }

func contextWithDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func pingTimeoutSeconds(ctx context.Context, fallback time.Duration) string {
	timeout := fallback
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return "1"
	}
	seconds := int(timeout / time.Second)
	if timeout%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
