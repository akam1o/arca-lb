package healthcheck

import (
	"io"
	"log/slog"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEngineSetCallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{}, nil, nil, logger)

	engine.vips["default/vip-1"] = &vipHealthState{
		vipKey: "default/vip-1",
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
	engine.SetCallback(func(vipKey, backendAddr string, oldState, newState V2BackendState) {
		called = true
		if vipKey != "default/vip-1" {
			t.Fatalf("vipKey = %q, want default/vip-1", vipKey)
		}
		if backendAddr != "10.0.0.1" {
			t.Fatalf("backendAddr = %q, want 10.0.0.1", backendAddr)
		}
		if oldState != V2StateUnknown || newState != V2StateUp {
			t.Fatalf("state transition = %s -> %s, want unknown -> up", oldState, newState)
		}
	})

	engine.handleResult(&probeResult{
		vipKey:      "default/vip-1",
		backendAddr: "10.0.0.1",
		success:     true,
		timestamp:   time.Now(),
	})

	if !called {
		t.Fatal("callback was not called")
	}
}

func TestEngineUsesNamespacedVIPKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{}, nil, nil, logger)
	backends := []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}}

	engine.vips["team-a/web"] = &vipHealthState{
		vipKey: "team-a/web",
		spec: &v1alpha1.HealthCheckSpec{
			RiseCount: 1,
			FallCount: 1,
		},
		backends: map[string]*backendHealthState{
			"10.0.0.1": {
				address: "10.0.0.1",
				state:   V2StateUp,
			},
		},
	}
	engine.vips["team-b/web"] = &vipHealthState{
		vipKey: "team-b/web",
		spec: &v1alpha1.HealthCheckSpec{
			RiseCount: 1,
			FallCount: 1,
		},
		backends: map[string]*backendHealthState{
			"10.0.0.1": {
				address: "10.0.0.1",
				state:   V2StateDown,
			},
		},
	}

	if got := engine.HealthyBackends("team-a/web", backends); len(got) != 1 {
		t.Fatalf("team-a/web healthy backends = %d, want 1", len(got))
	}
	if got := engine.HealthyBackends("team-b/web", backends); len(got) != 0 {
		t.Fatalf("team-b/web healthy backends = %d, want 0", len(got))
	}
}

func TestKeyForVIP(t *testing.T) {
	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "team-a",
			Name:      "web",
		},
	}

	if got := KeyForVIP(vip); got != "team-a/web" {
		t.Fatalf("KeyForVIP = %q, want team-a/web", got)
	}
}
