package healthcheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPingProberAppliesConfiguredTimeoutToCommandContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as fake ping")
	}

	dir := t.TempDir()
	pingPath := filepath.Join(dir, "ping")
	if err := os.WriteFile(pingPath, []byte("#!/bin/sh\nsleep 5\n"), 0755); err != nil {
		t.Fatalf("write fake ping: %v", err)
	}
	t.Setenv("PATH", dir)

	prober := &PingProber{timeout: 50 * time.Millisecond}

	start := time.Now()
	result := prober.Probe(context.Background(), "192.0.2.1")
	elapsed := time.Since(start)

	if result.Success {
		t.Fatal("Probe succeeded, want timeout failure")
	}
	if result.Error == nil {
		t.Fatal("Probe error = nil, want timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("Probe elapsed = %s, want timeout to stop fake ping quickly", elapsed)
	}
}
