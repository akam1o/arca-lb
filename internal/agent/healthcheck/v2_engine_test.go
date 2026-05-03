package healthcheck

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEngineStopReturns(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{WorkerCount: 1, MaxConcurrentChecks: 1}, nil, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		engine.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop did not return")
	}
}

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

func TestEngineIgnoresStaleProbeResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{}, nil, nil, logger)

	engine.vips["default/vip-1"] = &vipHealthState{
		vipKey: "default/vip-1",
		epoch:  2,
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
	engine.SetCallback(func(string, string, V2BackendState, V2BackendState) {
		called = true
	})

	engine.handleResult(&probeResult{
		vipKey:      "default/vip-1",
		epoch:       1,
		backendAddr: "10.0.0.1",
		success:     true,
		timestamp:   time.Now(),
	})

	if called {
		t.Fatal("callback was called for stale probe result")
	}
	states := engine.GetBackendStates("default/vip-1")
	if states["10.0.0.1"] != V2StateUnknown {
		t.Fatalf("backend state = %s, want %s", states["10.0.0.1"], V2StateUnknown)
	}
}

func TestEngineUpdateVIPFailurePreservesExistingState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{}, nil, nil, logger)
	engine.started = true
	engine.ctx = context.Background()

	var canceled bool
	prober := &recordingV2Prober{}
	engine.vips["default/vip-1"] = &vipHealthState{
		vipKey: "default/vip-1",
		epoch:  7,
		spec: &v1alpha1.HealthCheckSpec{
			Type:            v1alpha1.HCTypePing,
			IntervalSeconds: 5,
			TimeoutSeconds:  3,
			RiseCount:       1,
			FallCount:       1,
		},
		backends: map[string]*backendHealthState{
			"10.0.0.1": {
				address: "10.0.0.1",
				state:   V2StateUp,
			},
		},
		prober: prober,
		cancel: func() { canceled = true },
	}

	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "vip-1",
		},
		Spec: v1alpha1.VirtualIPSpec{
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
			HealthCheck: &v1alpha1.HealthCheckSpec{
				Type: v1alpha1.HCTypeHTTP,
			},
		},
	}

	if err := engine.UpdateVIP(vip); err == nil {
		t.Fatal("expected invalid health check update to fail")
	}
	if canceled {
		t.Fatal("existing health check was canceled after failed update")
	}
	if prober.closed {
		t.Fatal("existing prober was closed after failed update")
	}
	vs := engine.vips["default/vip-1"]
	if vs == nil {
		t.Fatal("existing VIP health state was removed after failed update")
	}
	if vs.epoch != 7 {
		t.Fatalf("VIP epoch = %d, want 7", vs.epoch)
	}
	if got := vs.backends["10.0.0.1"].state; got != V2StateUp {
		t.Fatalf("backend state = %s, want %s", got, V2StateUp)
	}
}

func TestEngineRejectsZeroHealthCheckTimingAndThresholds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{WorkerCount: 1, MaxConcurrentChecks: 1}, nil, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	tests := map[string]*v1alpha1.HealthCheckSpec{
		"interval": {Type: v1alpha1.HCTypePing, TimeoutSeconds: 1, RiseCount: 1, FallCount: 1},
		"timeout":  {Type: v1alpha1.HCTypePing, IntervalSeconds: 5, RiseCount: 1, FallCount: 1},
		"rise":     {Type: v1alpha1.HCTypePing, IntervalSeconds: 5, TimeoutSeconds: 3, FallCount: 1},
		"fall":     {Type: v1alpha1.HCTypePing, IntervalSeconds: 5, TimeoutSeconds: 3, RiseCount: 1},
	}

	for name, hc := range tests {
		t.Run(name, func(t *testing.T) {
			vip := &v1alpha1.VirtualIP{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "vip-" + name,
				},
				Spec: v1alpha1.VirtualIPSpec{
					Backends:    []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
					HealthCheck: hc,
				},
			}

			if err := engine.UpdateVIP(vip); err == nil {
				t.Fatal("expected zero-value health check field to be rejected")
			}
			if _, ok := engine.vips["default/vip-"+name]; ok {
				t.Fatal("invalid health check started a VIP scheduler")
			}
		})
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

func TestProbeTargetAddressUsesMonitorAddress(t *testing.T) {
	backend := v1alpha1.BackendSpec{
		Address:        "10.0.0.1",
		MonitorAddress: "192.0.2.10",
	}
	if got := probeTargetAddress(backend); got != "192.0.2.10" {
		t.Fatalf("probe target = %q, want monitor address", got)
	}

	backend.MonitorAddress = ""
	if got := probeTargetAddress(backend); got != "10.0.0.1" {
		t.Fatalf("probe target = %q, want backend address", got)
	}
}

func TestEngineProbeJobsUseMonitorAddress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := NewEngine(EngineConfig{MaxConcurrentChecks: 1}, nil, nil, logger)
	engine.jobCh = make(chan *probeJob, 1)

	vs := &vipHealthState{
		vipKey: "default/vip-1",
		epoch:  3,
		spec: &v1alpha1.HealthCheckSpec{
			TimeoutSeconds: 2,
		},
		backends: map[string]*backendHealthState{
			"10.0.0.1": {
				address:       "10.0.0.1",
				targetAddress: "192.0.2.10",
				state:         V2StateUnknown,
			},
		},
		prober: &recordingV2Prober{},
	}

	engine.emitProbeJobs(vs)

	select {
	case job := <-engine.jobCh:
		if job.backendAddr != "10.0.0.1" {
			t.Fatalf("job backend = %q, want 10.0.0.1", job.backendAddr)
		}
		if job.targetAddr != "192.0.2.10" {
			t.Fatalf("job target = %q, want 192.0.2.10", job.targetAddr)
		}
	default:
		t.Fatal("probe job was not emitted")
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

type recordingV2Prober struct {
	closed bool
}

func (p *recordingV2Prober) Probe(context.Context, string) V2ProbeResult {
	return V2ProbeResult{Success: true, Timestamp: time.Now()}
}

func (p *recordingV2Prober) Close() error {
	p.closed = true
	return nil
}
