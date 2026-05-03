package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	agentconfig "github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/agent/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAgentHTTPMuxServesHealthWhenMetricsDisabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newAgentHTTPMux(agentconfig.MetricsSettings{
		Enabled: false,
		Path:    "/metrics",
	}, logger)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("/health status = %d, want %d", resp.Code, http.StatusOK)
	}
	if body := resp.Body.String(); body != "ok" {
		t.Fatalf("/health body = %q, want ok", body)
	}

	resp = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("/metrics status = %d, want %d when metrics are disabled", resp.Code, http.StatusNotFound)
	}
}

func TestAgentHTTPMuxServesMetricsWhenEnabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newAgentHTTPMux(agentconfig.MetricsSettings{
		Enabled: true,
		Path:    "/metrics",
	}, logger)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want %d", resp.Code, http.StatusOK)
	}
}

func TestVIPEventHandlerWithdrawsRouteWhenHealthCheckUpdateFails(t *testing.T) {
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

	waitForCondition(t, func() bool {
		return dp.applyCount() == 2 && dp.backendCount() == 0 && !router.IsAnnounced(vip.Spec.Address)
	}, "invalid health check update to withdraw route")

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

func TestVIPEventHandlerIgnoresInvalidSpec(t *testing.T) {
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
	valid := &v1alpha1.VirtualIP{
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
		},
	}
	handler.OnVIPUpdate(valid)
	waitForCondition(t, func() bool {
		return dp.applyCount() == 1 && dp.backendCount() == 1 && router.IsAnnounced(valid.Spec.Address)
	}, "initial VIP reconcile")
	initialConditionUpdates := statusUpdater.updateCount()

	invalid := valid.DeepCopy()
	invalid.Generation = 3
	invalid.Spec.Backends = append(invalid.Spec.Backends, v1alpha1.BackendSpec{
		Address: "10.0.0.1",
		Weight:  100,
	})

	handler.OnVIPUpdate(invalid)

	if got := dp.applyCount(); got != 1 {
		t.Fatalf("dataplane ApplyVIP calls = %d, want 1 after invalid spec update", got)
	}
	if got := dp.backendCount(); got != 1 {
		t.Fatalf("dataplane backends = %d, want existing backend to remain", got)
	}
	if !router.IsAnnounced(valid.Spec.Address) {
		t.Fatal("route was withdrawn after invalid spec update")
	}
	if got := statusUpdater.updateCount(); got != initialConditionUpdates {
		t.Fatalf("health check condition updates = %d, want %d after invalid spec update", got, initialConditionUpdates)
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

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
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

func TestCleanupStaleLastConfigsReturnsErrorWhenCleanupFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	staleSpec := []byte(`{"address":"203.0.113.20","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-b/api", staleSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := &failingRouter{withdrawErr: errors.New("withdraw failed")}

	err = cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, nil, logger)
	if err == nil {
		t.Fatal("cleanupStaleLastConfigs error = nil, want cleanup failure")
	}
	if removed := dp.removedVIPs(); len(removed) != 0 {
		t.Fatalf("removed VIP count = %d, want 0 when route cleanup fails", len(removed))
	}
	stale, err := st.LoadLastConfig("team-b/api")
	if err != nil {
		t.Fatal(err)
	}
	if string(stale) != string(staleSpec) {
		t.Fatalf("stale config = %q, want retained %q", stale, staleSpec)
	}
}

func TestCleanupStaleLastConfigsCleansPendingConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	pendingSpec := []byte(`{"address":"203.0.113.20","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SavePendingConfig("team-b/api", pendingSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.20"); err != nil {
		t.Fatal(err)
	}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, nil, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want 1", len(removed))
	}
	if removed[0].Namespace != "team-b" || removed[0].Name != "api" || removed[0].Spec.Address != "203.0.113.20" {
		t.Fatalf("removed VIP = %#v", removed[0])
	}
	pending, err := st.LoadPendingConfig("team-b/api")
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatal("stale pending config should be deleted")
	}
	if router.IsAnnounced("203.0.113.20") {
		t.Fatal("stale pending route should be withdrawn")
	}
}

func TestCleanupStaleLastConfigsKeepsPendingCurrentAndCleansPreviousLastApplied(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	lastSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	pendingSpec := []byte(`{"address":"203.0.113.20","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", lastSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePendingConfig("team-a/web", pendingSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHealthState("team-a/web", "10.0.0.2", &store.BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.20",
			Port:     443,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.2", Weight: 100}},
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want previous last-applied VIP removed", len(removed))
	}
	if removed[0].Spec.Address != "203.0.113.10" {
		t.Fatalf("removed VIP address = %s, want previous address", removed[0].Spec.Address)
	}
	last, err := st.LoadLastConfig("team-a/web")
	if err != nil {
		t.Fatal(err)
	}
	if last != nil {
		t.Fatal("previous last-applied config should be deleted")
	}
	pending, err := st.LoadPendingConfig("team-a/web")
	if err != nil {
		t.Fatal(err)
	}
	if string(pending) != string(pendingSpec) {
		t.Fatalf("pending config = %q, want current pending config %q", pending, pendingSpec)
	}
	hc, err := st.LoadHealthState("team-a/web", "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if hc == nil {
		t.Fatal("current VIP health state should be preserved")
	}
	if router.IsAnnounced("203.0.113.10") {
		t.Fatal("previous route should be withdrawn")
	}
}

func TestCleanupStaleLastConfigsPreservesSharedCurrentAddress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	activeSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	staleSharedSpec := []byte(`{"address":"203.0.113.10","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", activeSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLastConfig("team-b/api", staleSharedSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
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

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want 1", len(removed))
	}
	if removed[0].Namespace != "team-b" || removed[0].Name != "api" || removed[0].Spec.Address != "203.0.113.10" {
		t.Fatalf("removed VIP = %#v", removed[0])
	}
	stale, err := st.LoadLastConfig("team-b/api")
	if err != nil {
		t.Fatal(err)
	}
	if stale != nil {
		t.Fatal("stale shared-address config should be deleted")
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("shared current address route should remain announced")
	}
}

func TestCleanupStaleLastConfigsWithdrawsRouteForInvalidCurrentAddress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	staleSharedSpec := []byte(`{"address":"203.0.113.10","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-b/api", staleSharedSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
				{Address: "10.0.0.1", Weight: 100},
			},
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want 1", len(removed))
	}
	if removed[0].Namespace != "team-b" || removed[0].Name != "api" || removed[0].Spec.Address != "203.0.113.10" {
		t.Fatalf("removed VIP = %#v", removed[0])
	}
	if router.IsAnnounced("203.0.113.10") {
		t.Fatal("invalid current VIP should not protect stale shared route")
	}
}

func TestCleanupStaleLastConfigsPreservesSameKeyWhenCurrentInvalid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	activeSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", activeSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.20",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
				{Address: "10.0.0.1", Weight: 100},
			},
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	if removed := dp.removedVIPs(); len(removed) != 0 {
		t.Fatalf("removed VIP count = %d, want 0 while same-key current spec is invalid", len(removed))
	}
	active, err := st.LoadLastConfig("team-a/web")
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(activeSpec) {
		t.Fatalf("active config = %q, want %q", active, activeSpec)
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("last known good route should remain announced")
	}
}

func TestCleanupStaleLastConfigsPreservesSiblingRouteForInvalidCurrentRetainedAddress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	activeSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	staleSharedSpec := []byte(`{"address":"203.0.113.10","port":443,"protocol":"TCP","backends":[{"address":"10.0.0.2","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", activeSpec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLastConfig("team-b/api", staleSharedSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.20",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
				{Address: "10.0.0.1", Weight: 100},
			},
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	removed := dp.removedVIPs()
	if len(removed) != 1 {
		t.Fatalf("removed VIP count = %d, want only stale sibling removed", len(removed))
	}
	if removed[0].Namespace != "team-b" || removed[0].Name != "api" || removed[0].Spec.Address != "203.0.113.10" {
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
		t.Fatal("stale sibling config should be deleted")
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("preserved invalid-current address route should protect sibling cleanup")
	}
}

func TestCleanupStaleLastConfigsPreservesSameKeyWhenCurrentHealthCheckInvalid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	activeSpec := []byte(`{"address":"203.0.113.10","port":80,"protocol":"TCP","backends":[{"address":"10.0.0.1","weight":100}]}`)
	if err := st.SaveLastConfig("team-a/web", activeSpec); err != nil {
		t.Fatal(err)
	}

	dp := &recordingDataPlane{}
	router := routing.NewNoop()
	if err := router.AnnounceVIP(context.Background(), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	current := []v1alpha1.VirtualIP{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.20",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
			HealthCheck: &v1alpha1.HealthCheckSpec{
				Type: v1alpha1.HCTypeHTTP,
			},
		},
	}}

	if err := cleanupStaleLastConfigs(context.Background(), st, dp, router, nil, current, logger); err != nil {
		t.Fatalf("cleanupStaleLastConfigs: %v", err)
	}

	if removed := dp.removedVIPs(); len(removed) != 0 {
		t.Fatalf("removed VIP count = %d, want 0 while same-key current health check is invalid", len(removed))
	}
	active, err := st.LoadLastConfig("team-a/web")
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != string(activeSpec) {
		t.Fatalf("active config = %q, want %q", active, activeSpec)
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("last known good route should remain announced")
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

type failingRouter struct {
	withdrawErr error
}

func (r *failingRouter) AnnounceVIP(context.Context, string) error {
	return nil
}

func (r *failingRouter) WithdrawVIP(context.Context, string) error {
	return r.withdrawErr
}

func (r *failingRouter) IsAnnounced(string) bool {
	return false
}

func (r *failingRouter) Close() error {
	return nil
}

type recordingHealthCheckConditionUpdater struct {
	mu        sync.Mutex
	condition metav1.Condition
	updates   int
}

func (r *recordingHealthCheckConditionUpdater) UpdateHealthCheckCondition(_ context.Context, _ *v1alpha1.VirtualIP, condition metav1.Condition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.condition = condition
	r.updates++
	return nil
}

func (r *recordingHealthCheckConditionUpdater) lastCondition() metav1.Condition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.condition
}

func (r *recordingHealthCheckConditionUpdater) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates
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
