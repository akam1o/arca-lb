package healthcheck

import (
	"context"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"
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
