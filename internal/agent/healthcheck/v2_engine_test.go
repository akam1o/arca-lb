package healthcheck

import (
	"io"
	"log/slog"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

func TestEngineSetCallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{}, nil, nil, logger)

	engine.vips["vip-1"] = &vipHealthState{
		vipName: "vip-1",
		spec: &v1alpha1.HealthCheckSpec{
			RiseCount: 1,
			FallCount: 1,
		},
		backends: map[string]*backendHealthState{
			"10.0.0.1": {
				address: "10.0.0.1",
				state:   V2StateUnknown,
			},
		},
	}

	var called bool
	engine.SetCallback(func(vipName, backendAddr string, oldState, newState V2BackendState) {
		called = true
		if vipName != "vip-1" {
			t.Fatalf("vipName = %q, want vip-1", vipName)
		}
		if backendAddr != "10.0.0.1" {
			t.Fatalf("backendAddr = %q, want 10.0.0.1", backendAddr)
		}
		if oldState != V2StateUnknown || newState != V2StateUp {
			t.Fatalf("state transition = %s -> %s, want unknown -> up", oldState, newState)
		}
	})

	engine.handleResult(&probeResult{
		vipName:     "vip-1",
		backendAddr: "10.0.0.1",
		success:     true,
		timestamp:   time.Now(),
	})

	if !called {
		t.Fatal("callback was not called")
	}
}
