package healthcheck

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

func TestBuildHTTPProbeURLIPv6(t *testing.T) {
	got, err := buildHTTPProbeURL("http", "2001:db8::1", 8080, "/healthz?ready=1")
	if err != nil {
		t.Fatalf("buildHTTPProbeURL: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if parsed.Host != "[2001:db8::1]:8080" {
		t.Fatalf("URL host = %q, want [2001:db8::1]:8080", parsed.Host)
	}
	if parsed.Path != "/healthz" {
		t.Fatalf("URL path = %q, want /healthz", parsed.Path)
	}
	if parsed.RawQuery != "ready=1" {
		t.Fatalf("URL query = %q, want ready=1", parsed.RawQuery)
	}
}

func TestHTTPProberFromSpecUsesTLS12MinimumForHTTPS(t *testing.T) {
	prober, err := newHTTPProberFromSpec(&v1alpha1.HTTPHealthCheck{
		Port: 443,
	}, true)
	if err != nil {
		t.Fatalf("newHTTPProberFromSpec: %v", err)
	}
	defer func() { _ = prober.Close() }()

	transport, ok := prober.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", prober.client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want TLS 1.2", transport.TLSClientConfig.MinVersion)
	}
}

func TestHTTPProberDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/ok":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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

	prober, err := newHTTPProberFromSpec(&v1alpha1.HTTPHealthCheck{
		Port:          port,
		Path:          "/redirect",
		ExpectedCodes: []int{http.StatusFound},
	}, false)
	if err != nil {
		t.Fatalf("newHTTPProberFromSpec: %v", err)
	}
	defer func() { _ = prober.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := prober.Probe(ctx, host)
	if !result.Success {
		t.Fatalf("redirect probe failed: status=%d error=%v", result.StatusCode, result.Error)
	}
	if result.StatusCode != http.StatusFound {
		t.Fatalf("status code = %d, want %d", result.StatusCode, http.StatusFound)
	}
}

func TestTCPProbeAddressIPv6(t *testing.T) {
	if got := tcpProbeAddress("2001:db8::1", 8080); got != "[2001:db8::1]:8080" {
		t.Fatalf("tcpProbeAddress = %q, want [2001:db8::1]:8080", got)
	}
}

func TestTCPProberReadHonorsContextDeadline(t *testing.T) {
	target, port := startSilentTCPServer(t)
	prober := &tcpProber{port: port, expectedResponse: "OK"}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := prober.Probe(ctx, target)
	elapsed := time.Since(start)

	if result.Success {
		t.Fatal("expected probe to fail when backend does not send expected response")
	}
	if result.Error == nil {
		t.Fatal("expected probe error")
	}
	if elapsed > time.Second {
		t.Fatalf("probe took %s, want it to return when context deadline expires", elapsed)
	}
}

func TestTCPProberReadHonorsContextCancel(t *testing.T) {
	target, port := startSilentTCPServer(t)
	prober := &tcpProber{port: port, expectedResponse: "OK"}

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	start := time.Now()
	result := prober.Probe(ctx, target)
	elapsed := time.Since(start)

	if result.Success {
		t.Fatal("expected probe to fail when context is cancelled")
	}
	if result.Error == nil {
		t.Fatal("expected probe error")
	}
	if elapsed > time.Second {
		t.Fatalf("probe took %s, want it to return when context is cancelled", elapsed)
	}
}

func TestNewProberFromSpecTLSHello(t *testing.T) {
	prober, err := newProberFromSpec(&v1alpha1.HealthCheckSpec{
		Type: v1alpha1.HCTypeTLSHello,
		TCP:  &v1alpha1.TCPHealthCheck{Port: 443},
	})
	if err != nil {
		t.Fatalf("newProberFromSpec: %v", err)
	}
	if _, ok := prober.(*tlsHelloProber); !ok {
		t.Fatalf("prober type = %T, want *tlsHelloProber", prober)
	}
}

func TestTLSHelloProberSucceedsWithTLSServer(t *testing.T) {
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

	prober := &tlsHelloProber{port: port}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := prober.Probe(ctx, host)
	if !result.Success {
		t.Fatalf("TLS hello probe failed: %v", result.Error)
	}
}

func TestPingTimeoutSecondsHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()

	if got := pingTimeoutSeconds(ctx, 2*time.Second); got != "4" {
		t.Fatalf("ping timeout = %q, want 4 second ceil of context deadline", got)
	}
}

func TestPingTimeoutSecondsUsesFallbackWithoutDeadline(t *testing.T) {
	ctx, cancel := contextWithDefaultTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if got := pingTimeoutSeconds(ctx, 2*time.Second); got != "2" {
		t.Fatalf("ping timeout = %q, want fallback 2 seconds", got)
	}
}

func startSilentTCPServer(t *testing.T) (string, int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
	)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}
