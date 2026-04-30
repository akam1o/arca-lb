package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestVIPEventHandlerSkipsReconcileWhenHealthCheckUpdateFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hcEngine := healthcheck.NewEngine(healthcheck.EngineConfig{WorkerCount: 1, MaxConcurrentChecks: 1}, nil, nil, logger)
	if err := hcEngine.Start(ctx); err != nil {
		t.Fatalf("Start health check engine: %v", err)
	}
	defer hcEngine.Stop()

	dp := &recordingDataPlane{}
	reconMgr := reconciler.NewManager(dp, routing.NewNoop(), nil, hcEngine, time.Hour, logger)
	reconMgr.Start(ctx)
	defer reconMgr.Stop()

	handler := &vipEventHandler{
		reconciler: reconMgr,
		hcEngine:   hcEngine,
		logger:     logger,
	}
	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "web",
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
			HealthCheck: &v1alpha1.HealthCheckSpec{
				Type: v1alpha1.HCTypeHTTP,
			},
		},
	}

	handler.OnVIPUpdate(vip)

	if got := len(reconMgr.GetStatus()); got != 0 {
		t.Fatalf("managed VIPs = %d, want 0 after health check update failure", got)
	}
	if got := dp.applyCount(); got != 0 {
		t.Fatalf("dataplane ApplyVIP calls = %d, want 0 after health check update failure", got)
	}
}

type recordingDataPlane struct {
	mu      sync.Mutex
	applies int
}

func (r *recordingDataPlane) ApplyVIP(context.Context, *v1alpha1.VirtualIP, []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applies++
	return nil
}

func (r *recordingDataPlane) RemoveVIP(context.Context, *v1alpha1.VirtualIP) error {
	return nil
}

func (r *recordingDataPlane) SetBackends(context.Context, *v1alpha1.VirtualIP, []v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) AddBackend(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) RemoveBackend(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) GetState(context.Context) (*dataplane.State, error) {
	return &dataplane.State{}, nil
}

func (r *recordingDataPlane) Close() error {
	return nil
}

func (r *recordingDataPlane) applyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applies
}
