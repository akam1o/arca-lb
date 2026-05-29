package healthcheck

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHealthCheckProbersRejectInvalidTargets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tests := []struct {
		name  string
		probe func(context.Context, string) error
	}{
		{
			name: "v1 http",
			probe: func(ctx context.Context, target string) error {
				result := (&HTTPProber{port: 80, path: "/", method: "GET"}).Probe(ctx, target)
				return result.Error
			},
		},
		{
			name: "v1 tcp",
			probe: func(ctx context.Context, target string) error {
				result := (&TCPProber{port: 80}).Probe(ctx, target)
				return result.Error
			},
		},
		{
			name: "v1 tls hello",
			probe: func(ctx context.Context, target string) error {
				result := (&TLSHelloProber{port: 443, timeout: time.Second}).Probe(ctx, target)
				return result.Error
			},
		},
		{
			name: "v2 http url builder",
			probe: func(_ context.Context, target string) error {
				_, err := buildHTTPProbeURL("http", target, 80, "/")
				return err
			},
		},
		{
			name: "v2 tcp",
			probe: func(ctx context.Context, target string) error {
				result := (&tcpProber{port: 80}).Probe(ctx, target)
				return result.Error
			},
		},
		{
			name: "v2 tls hello",
			probe: func(ctx context.Context, target string) error {
				result := (&tlsHelloProber{port: 443}).Probe(ctx, target)
				return result.Error
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.probe(ctx, "backend.example")
			if err == nil {
				t.Fatal("probe accepted invalid target")
			}
			if !strings.Contains(err.Error(), "invalid health check target address") {
				t.Fatalf("error = %v, want invalid target error", err)
			}
		})
	}
}
