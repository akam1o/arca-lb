package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/agent/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestVIPEventHandlerPreservesDataplaneWhenHealthCheckUpdateFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hcEngine := healthcheck.NewEngine(healthcheck.EngineConfig{WorkerCount: 1, MaxConcurrentChecks: 1}, nil, nil, logger)
	if err := hcEngine.Start(ctx); err != nil {
		t.Fatalf("Start health check engine: %v", err)
	}
	defer hcEngine.Stop()

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	statusUpdater := &recordingHealthCheckConditionUpdater{}
	reconMgr := reconciler.NewManager(dp, router, nil, hcEngine, time.Hour, logger)
	reconMgr.Start(ctx)
	defer reconMgr.Stop()

	handler := &vipEventHandler{
		ctx:           ctx,
		reconciler:    reconMgr,
		hcEngine:      hcEngine,
		statusUpdater: statusUpdater,
		logger:        logger,
	}
	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			Generation: 2,
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

	initial := vip.DeepCopy()
	initial.Spec.HealthCheck = nil
	handler.OnVIPUpdate(initial)
	waitForCondition(t, func() bool {
		return dp.applyCount() == 1 && dp.backendCount() == 1 && router.IsAnnounced(initial.Spec.Address)
	}, "initial VIP reconcile")

	handler.OnVIPUpdate(vip)

	if got := dp.applyCount(); got != 1 {
		t.Fatalf("dataplane ApplyVIP calls = %d, want 1 after invalid health check update", got)
	}
	if got := dp.backendCount(); got != 1 {
		t.Fatalf("dataplane backends = %d, want existing backend to remain", got)
	}
	if !router.IsAnnounced(vip.Spec.Address) {
		t.Fatal("route was withdrawn after invalid health check update")
	}

	condition := statusUpdater.lastCondition()
	if condition.Type != agentstatus.ConditionHealthCheckReady {
		t.Fatalf("condition type = %q, want %q", condition.Type, agentstatus.ConditionHealthCheckReady)
	}
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("condition status = %s, want False", condition.Status)
	}
	if condition.Reason != "InvalidHealthCheck" {
		t.Fatalf("condition reason = %q, want InvalidHealthCheck", condition.Reason)
	}
	if condition.ObservedGeneration != vip.Generation {
		t.Fatalf("condition observedGeneration = %d, want %d", condition.ObservedGeneration, vip.Generation)
	}
}

func TestCleanupStaleLastConfigsRemovesOnlyMissingVIPs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	activeSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	staleSpec := []byte(`{"address":"203.0.113.20","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", activeSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLastConfig("team-b/api", staleSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHealthState("team-b/api", "10.0.0.2", &store.BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	if err := router.AnnounceVIP(context.Background(), "203.0.113.20"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want 1", len(removed))
	}
	if removed[0].Namespace != "team-b" || removed[0].Name != "api" || removed[0].Spec.Address != "203.0.113.20" {
		t.Fatalf("removed VIP = %#v", removed[0])
	}

	active, err := st.LoadLastConfig("team-a/web")
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(activeSpec) {
		t.Fatalf("active config = %q, want %q", active, activeSpec)
	}
	stale, err := st.LoadLastConfig("team-b/api")
	if err != nil {
		t.Fatal(err)
	}
	if stale != nil {
		t.Fatal("stale config should be deleted")
	}
	hc, err := st.LoadHealthState("team-b/api", "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if hc != nil {
		t.Fatal("stale health state should be deleted")
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("active route should remain announced")
	}
	if router.IsAnnounced("203.0.113.20") {
		t.Fatal("stale route should be withdrawn")
	}
}

type recordingDataPlane struct {
	mu       sync.Mutex
	applies  int
	backends []v1alpha1.BackendSpec
	removed  []v1alpha1.VirtualIP
}

func (r *recordingDataPlane) ApplyVIP(_ context.Context, _ *v1alpha1.VirtualIP, backends []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applies++
	r.backends = append([]v1alpha1.BackendSpec(nil), backends...)
	return nil
}

func (r *recordingDataPlane) RemoveVIP(_ context.Context, vip *v1alpha1.VirtualIP) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, *vip.DeepCopy())
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

func (r *recordingDataPlane) backendCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.backends)
}

func (r *recordingDataPlane) removedVIPs() []v1alpha1.VirtualIP {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]v1alpha1.VirtualIP(nil), r.removed...)
}

type recordingHealthCheckConditionUpdater struct {
	mu        sync.Mutex
	condition metav1.Condition
}

func (r *recordingHealthCheckConditionUpdater) UpdateHealthCheckCondition(_ context.Context, _ *v1alpha1.VirtualIP, condition metav1.Condition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.condition = condition
	return nil
}

func (r *recordingHealthCheckConditionUpdater) lastCondition() metav1.Condition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.condition
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
