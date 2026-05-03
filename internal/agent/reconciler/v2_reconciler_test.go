package reconciler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestV2ManagerRecreatesVIPAfterDelete(t *testing.T) {
	dp := newRecordingDataPlane()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, routing.NewNoop(), nil, nil, time.Hour, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)
	waitFor(t, func() bool { return dp.applyCount() == 1 }, "initial VIP apply")

	mgr.OnVIPDelete(vip)
	waitFor(t, func() bool { return dp.removeCount() == 1 }, "VIP removal")
	waitFor(t, func() bool { return len(mgr.GetStatus()) == 0 }, "manager to drop deleted VIP")

	recreated := newV2TestVIP("default", "web", "uid-2")
	mgr.OnVIPUpdate(recreated)
	waitFor(t, func() bool { return dp.applyCount() == 2 }, "recreated VIP apply")
}

func TestV2ReconcilerReportsStatusAndWithdrawsRouteWhenDataPlaneFails(t *testing.T) {
	dp := newRecordingDataPlane()
	dp.applyErr = errors.New("apply failed")
	router := routing.NewNoop()
	statusUpdater := &recordingStatusUpdater{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetStatusUpdater(statusUpdater)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)
	waitFor(t, func() bool { return dp.applyCount() == 1 }, "failed VIP apply")

	if router.IsAnnounced(vip.Spec.Address) {
		t.Fatal("route was announced despite dataplane apply failure")
	}
	if got := statusUpdater.updateCount(); got != 1 {
		t.Fatalf("status updates = %d, want 1 after dataplane apply failure", got)
	}
	serving := findTestCondition(statusUpdater.lastConditions(), agentstatus.ConditionServing)
	if serving == nil {
		t.Fatal("Serving condition missing")
	}
	if serving.Status != metav1.ConditionUnknown {
		t.Fatalf("Serving status = %s, want Unknown", serving.Status)
	}
	if serving.Reason != "DataPlaneApplyFailed" {
		t.Fatalf("Serving reason = %q, want DataPlaneApplyFailed", serving.Reason)
	}
}

func TestV2ReconcilerReportsRouteFailureInStatus(t *testing.T) {
	events := &eventRecorder{}
	dp := newRecordingDataPlane()
	router := newRecordingRouter(events)
	router.announceErr = errors.New("vtysh failed")
	statusUpdater := &recordingStatusUpdater{events: events}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetStatusUpdater(statusUpdater)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)
	waitFor(t, func() bool { return statusUpdater.updateCount() == 1 }, "status update after route failure")

	gotEvents := events.snapshot()
	wantEvents := []string{"announce:203.0.113.10", "status"}
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("events = %#v, want %#v", gotEvents, wantEvents)
	}
	for i := range wantEvents {
		if gotEvents[i] != wantEvents[i] {
			t.Fatalf("events = %#v, want %#v", gotEvents, wantEvents)
		}
	}
	if router.IsAnnounced(vip.Spec.Address) {
		t.Fatal("route should not be recorded as announced after announce failure")
	}

	conditions := statusUpdater.lastConditions()
	route := findTestCondition(conditions, agentstatus.ConditionRouteAdvertised)
	if route == nil {
		t.Fatalf("RouteAdvertised condition missing from %#v", conditions)
	}
	if route.Status != metav1.ConditionUnknown {
		t.Fatalf("RouteAdvertised status = %s, want Unknown", route.Status)
	}
	if route.Reason != "RouteUpdateFailed" {
		t.Fatalf("RouteAdvertised reason = %q, want RouteUpdateFailed", route.Reason)
	}
}

func TestV2ReconcilerRollingRecreatesTuningDrift(t *testing.T) {
	events := &eventRecorder{}
	dp := newRecordingDataPlane()
	dp.events = events
	dp.drifts = []dataplane.VIPTuningDrift{{
		Field:   "new_flows_table_length",
		Current: "1024",
		Desired: "2048",
	}}
	router := newRecordingRouter(events)
	rollouts := &recordingRolloutCoordinator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetRolloutCoordinator(rollouts)
	mgr.SetTuningDriftConfig(TuningDriftConfig{
		Policy:        TuningDriftPolicyRollingRecreate,
		DrainDuration: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)

	waitFor(t, func() bool {
		return dp.recreateCount() == 1 && router.announceCount() == 1
	}, "rolling VIP recreate")

	got := events.snapshot()
	want := []string{
		"withdraw:203.0.113.10",
		"recreate:default/web",
		"announce:203.0.113.10",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %#v, want %#v", got, want)
		}
	}
	if !router.IsAnnounced(vip.Spec.Address) {
		t.Fatal("route should be re-announced after VIP recreate")
	}
	if drifts := dp.TuningDrifts("default/web"); len(drifts) != 0 {
		t.Fatalf("drifts after recreate = %#v, want none", drifts)
	}
	if got := rollouts.keysSnapshot(); len(got) != 1 || got[0] != "vip-address/203.0.113.10" {
		t.Fatalf("rollout keys = %#v, want address-scoped key", got)
	}
}

func TestV2ReconcilerSkipsRollingRecreateWhenSharedAddressIsServing(t *testing.T) {
	events := &eventRecorder{}
	dp := newRecordingDataPlane()
	dp.events = events
	router := newRecordingRouter(events)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetTuningDriftConfig(TuningDriftConfig{
		Policy:        TuningDriftPolicyRollingRecreate,
		DrainDuration: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	web443 := newV2TestVIP("default", "web-443", "uid-443")
	web443.Spec.Port = 443
	mgr.OnVIPUpdate(web443)
	waitFor(t, func() bool { return router.announceCount() == 1 }, "shared VIP route announcement")

	web80 := newV2TestVIP("default", "web-80", "uid-80")
	web80.Spec.Port = 80
	mgr.OnVIPUpdate(web80)
	waitFor(t, func() bool { return dp.applyCount() == 2 }, "shared VIP listener apply")

	dp.setDrifts([]dataplane.VIPTuningDrift{{
		Field:   "new_flows_table_length",
		Current: "1024",
		Desired: "2048",
	}})
	if err := mgr.Reconcile("default/web-80"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	waitFor(t, func() bool { return dp.applyCount() == 3 }, "tuning drift reconcile")

	if got := dp.recreateCount(); got != 0 {
		t.Fatalf("recreate count = %d, want 0 while sibling keeps shared address serving", got)
	}
	if got := router.withdrawCount(); got != 0 {
		t.Fatalf("withdraw count = %d, want 0 while sibling keeps shared address serving", got)
	}
	if !router.IsAnnounced(web80.Spec.Address) {
		t.Fatal("shared VIP address route should remain advertised")
	}
	if drifts := dp.TuningDrifts("default/web-80"); len(drifts) == 0 {
		t.Fatal("tuning drift should remain pending when rolling recreate is skipped")
	}

	got := events.snapshot()
	want := []string{"announce:203.0.113.10"}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %#v, want %#v", got, want)
		}
	}
}

func TestRouteCoordinatorSuppressesAnnouncementsDuringAddressDrain(t *testing.T) {
	events := &eventRecorder{}
	router := newRecordingRouter(events)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := newRouteCoordinator(router, logger)
	ctx := context.Background()

	advertised, err := routes.SetServing(ctx, "default/web-80", "203.0.113.10", true)
	if err != nil {
		t.Fatalf("SetServing: %v", err)
	}
	if !advertised {
		t.Fatal("initial serving VIP should advertise the address route")
	}

	drained, err := routes.BeginDrain(ctx, "default/web-80", "203.0.113.10")
	if err != nil {
		t.Fatalf("BeginDrain: %v", err)
	}
	if !drained {
		t.Fatal("address should drain when no sibling is serving")
	}
	if router.IsAnnounced("203.0.113.10") {
		t.Fatal("address route should be withdrawn during drain")
	}

	advertised, err = routes.SetServing(ctx, "default/web-443", "203.0.113.10", true)
	if err != nil {
		t.Fatalf("SetServing sibling: %v", err)
	}
	if advertised {
		t.Fatal("serving sibling should not re-advertise while address drain is held")
	}
	if router.IsAnnounced("203.0.113.10") {
		t.Fatal("address route should stay withdrawn while drain is held")
	}

	advertised, err = routes.FinishDrain(ctx, "default/web-80", "203.0.113.10")
	if err != nil {
		t.Fatalf("FinishDrain: %v", err)
	}
	if !advertised {
		t.Fatal("serving sibling should advertise after address drain is released")
	}

	got := events.snapshot()
	want := []string{
		"announce:203.0.113.10",
		"withdraw:203.0.113.10",
		"announce:203.0.113.10",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %#v, want %#v", got, want)
		}
	}
}

func TestV2ManagerKeepsSharedAddressAdvertisedForHealthySibling(t *testing.T) {
	dp := newRecordingDataPlane()
	router := newRecordingRouter(nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	web80 := newV2TestVIP("default", "web-80", "uid-80")
	web80.Spec.Port = 80
	mgr.OnVIPUpdate(web80)
	waitFor(t, func() bool { return router.announceCount() == 1 }, "shared VIP route announcement")

	web443 := newV2TestVIP("default", "web-443", "uid-443")
	web443.Spec.Port = 443
	web443.Spec.Backends = nil
	mgr.OnVIPUpdate(web443)
	waitFor(t, func() bool { return dp.applyCount() == 2 }, "unhealthy sibling VIP apply")

	if got := router.withdrawCount(); got != 0 {
		t.Fatalf("withdraw count = %d, want 0 while web-80 is serving", got)
	}
	if !router.IsAnnounced(web80.Spec.Address) {
		t.Fatal("shared VIP address route should remain advertised while web-80 is serving")
	}

	mgr.OnVIPDelete(web443)
	waitFor(t, func() bool { return dp.removeCount() == 1 }, "unhealthy sibling VIP removal")
	if got := router.withdrawCount(); got != 0 {
		t.Fatalf("withdraw count = %d, want 0 after deleting non-serving sibling", got)
	}

	mgr.OnVIPDelete(web80)
	waitFor(t, func() bool { return router.withdrawCount() == 1 }, "shared VIP route withdrawal after last serving VIP")
	if router.IsAnnounced(web80.Spec.Address) {
		t.Fatal("shared VIP address route should be withdrawn after last serving VIP is deleted")
	}
}

func TestV2ReconcilerDrainsPreviousAddressBeforeDataplaneUpdateAndRetries(t *testing.T) {
	dp := newRecordingDataPlane()
	router := newRecordingRouter(nil)
	statusUpdater := &recordingStatusUpdater{}
	rollouts := &recordingRolloutCoordinator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetStatusUpdater(statusUpdater)
	mgr.SetRolloutCoordinator(rollouts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)
	waitFor(t, func() bool {
		return router.IsAnnounced("203.0.113.10") && statusUpdater.updateCount() == 1
	}, "initial route announcement")

	router.withdrawErr = errors.New("vtysh withdraw failed")
	updated := vip.DeepCopy()
	updated.Generation = 2
	updated.Spec.Address = "203.0.113.20"
	mgr.OnVIPUpdate(updated)

	waitFor(t, func() bool { return statusUpdater.updateCount() == 2 }, "status update after previous route withdraw failure")
	if got := dp.applyCount(); got != 1 {
		t.Fatalf("apply count = %d, want no dataplane update after previous address withdraw failure", got)
	}
	if got := dp.lastAppliedVIP().Spec.Address; got != "203.0.113.10" {
		t.Fatalf("last applied VIP address = %s, want old address", got)
	}
	if got := router.announceCount(); got != 1 {
		t.Fatalf("announce count = %d, want only the initial route announcement", got)
	}
	if got := router.withdrawCount(); got != 1 {
		t.Fatalf("withdraw count = %d, want one failed previous address withdraw", got)
	}
	if got := rollouts.callCount(); got != 2 {
		t.Fatalf("rollout lock calls = %d, want old and new address locks for one failed rollout attempt", got)
	}
	if !router.IsAnnounced("203.0.113.10") {
		t.Fatal("old VIP address should remain tracked as announced after withdraw failure")
	}
	if router.IsAnnounced("203.0.113.20") {
		t.Fatal("new VIP address should not be announced until previous address withdraw succeeds")
	}

	route := findTestCondition(statusUpdater.lastConditions(), agentstatus.ConditionRouteAdvertised)
	if route == nil {
		t.Fatal("RouteAdvertised condition missing")
	}
	if route.Status != metav1.ConditionUnknown {
		t.Fatalf("RouteAdvertised status = %s, want Unknown", route.Status)
	}
	if route.Reason != "RouteUpdateFailed" {
		t.Fatalf("RouteAdvertised reason = %q, want RouteUpdateFailed", route.Reason)
	}

	router.withdrawErr = nil
	if err := mgr.Reconcile("default/web"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	waitFor(t, func() bool {
		return dp.applyCount() == 2 && router.withdrawCount() == 2 && router.IsAnnounced("203.0.113.20")
	}, "previous route withdraw retry and new route announcement")

	if router.IsAnnounced("203.0.113.10") {
		t.Fatal("old VIP address should be withdrawn after retry succeeds")
	}
	if got := dp.lastAppliedVIP().Spec.Address; got != "203.0.113.20" {
		t.Fatalf("last applied VIP address = %s, want new address", got)
	}
	if got := rollouts.callCount(); got != 4 {
		t.Fatalf("rollout lock calls = %d, want failed attempt and retry", got)
	}
	if got := rollouts.keysSnapshot(); !sameStringSlice(got, []string{
		"vip-address/203.0.113.10",
		"vip-address/203.0.113.20",
		"vip-address/203.0.113.10",
		"vip-address/203.0.113.20",
	}) {
		t.Fatalf("rollout keys = %#v, want old and new VIP address keys", got)
	}
}

func TestV2ReconcilerSkipsStaleAddressRolloutAfterWaitingForLock(t *testing.T) {
	dp := newRecordingDataPlane()
	router := newRecordingRouter(nil)
	rollouts := newBlockingRolloutCoordinator()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewManager(dp, router, nil, nil, time.Hour, logger)
	mgr.SetRolloutCoordinator(rollouts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	vip := newV2TestVIP("default", "web", "uid-1")
	mgr.OnVIPUpdate(vip)
	waitFor(t, func() bool {
		return router.IsAnnounced("203.0.113.10") && dp.applyCount() == 1
	}, "initial route announcement")

	stale := vip.DeepCopy()
	stale.Generation = 2
	stale.Spec.Address = "203.0.113.20"
	mgr.OnVIPUpdate(stale)
	rollouts.waitForFirstCall(t)

	current := vip.DeepCopy()
	current.Generation = 3
	current.Spec.Address = "203.0.113.30"
	mgr.OnVIPUpdate(current)
	rollouts.releaseFirst()

	waitFor(t, func() bool {
		last := dp.lastAppliedVIP()
		return dp.applyCount() == 2 && last != nil && last.Spec.Address == "203.0.113.30"
	}, "latest address rollout")

	if router.IsAnnounced("203.0.113.20") {
		t.Fatal("stale desired address should not be announced after rollout lock wait")
	}
	if got := rollouts.callCount(); got != 4 {
		t.Fatalf("rollout lock calls = %d, want stale attempt and latest retry", got)
	}
}

func TestV2ReconcilerCoalescesBurstUpdatesToLatestSpec(t *testing.T) {
	dp := newRecordingDataPlane()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := routing.NewNoop()
	vr := newVIPReconciler(
		"default/web",
		dp,
		newRouteCoordinator(router, logger),
		nil,
		nil,
		nil,
		nil,
		time.Hour,
		TuningDriftConfig{},
		logger,
		nil,
	)

	for i := 1; i <= 20; i++ {
		vip := newV2TestVIP("default", "web", "uid-1")
		vip.Generation = int64(i)
		vip.Spec.Port = 80 + i
		vr.update(vip)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go vr.run(ctx)
	defer vr.stop()

	waitFor(t, func() bool {
		vip := dp.lastAppliedVIP()
		return vip != nil && vip.Generation == 20 && vip.Spec.Port == 100
	}, "latest VIP spec to be applied after burst updates")
}

func newV2TestVIP(namespace, name string, uid types.UID) *v1alpha1.VirtualIP {
	return &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uid,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
			},
		},
	}
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (e *eventRecorder) record(event string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *eventRecorder) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func waitFor(t *testing.T, condition func() bool, description string) {
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

func findTestCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type recordingDataPlane struct {
	mu        sync.Mutex
	applies   int
	removals  int
	applyErr  error
	recreates int
	drifts    []dataplane.VIPTuningDrift
	events    *eventRecorder
	lastVIP   *v1alpha1.VirtualIP
}

func newRecordingDataPlane() *recordingDataPlane {
	return &recordingDataPlane{}
}

func (r *recordingDataPlane) ApplyVIP(_ context.Context, vip *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applies++
	r.lastVIP = vip.DeepCopy()
	return r.applyErr
}

func (r *recordingDataPlane) RemoveVIP(_ context.Context, _ *v1alpha1.VirtualIP) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removals++
	return nil
}

func (r *recordingDataPlane) SetBackends(_ context.Context, _ *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) AddBackend(_ context.Context, _ *v1alpha1.VirtualIP, _ v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) RemoveBackend(_ context.Context, _ *v1alpha1.VirtualIP, _ v1alpha1.BackendSpec) error {
	return nil
}

func (r *recordingDataPlane) GetState(_ context.Context) (*dataplane.State, error) {
	return &dataplane.State{}, nil
}

func (r *recordingDataPlane) Close() error {
	return nil
}

func (r *recordingDataPlane) TuningDrifts(_ string) []dataplane.VIPTuningDrift {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dataplane.VIPTuningDrift(nil), r.drifts...)
}

func (r *recordingDataPlane) setDrifts(drifts []dataplane.VIPTuningDrift) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drifts = append([]dataplane.VIPTuningDrift(nil), drifts...)
}

func (r *recordingDataPlane) RecreateVIP(_ context.Context, vip *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recreates++
	r.drifts = nil
	r.events.record("recreate:" + vip.Namespace + "/" + vip.Name)
	return nil
}

func (r *recordingDataPlane) applyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applies
}

func (r *recordingDataPlane) lastAppliedVIP() *v1alpha1.VirtualIP {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastVIP == nil {
		return nil
	}
	return r.lastVIP.DeepCopy()
}

func (r *recordingDataPlane) removeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removals
}

func (r *recordingDataPlane) recreateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recreates
}

type recordingRouter struct {
	mu          sync.Mutex
	announced   map[string]bool
	announces   int
	withdraws   int
	announceErr error
	withdrawErr error
	events      *eventRecorder
}

func newRecordingRouter(events *eventRecorder) *recordingRouter {
	return &recordingRouter{
		announced: make(map[string]bool),
		events:    events,
	}
}

func (r *recordingRouter) AnnounceVIP(_ context.Context, vipAddress string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.announces++
	r.events.record("announce:" + vipAddress)
	if r.announceErr != nil {
		return r.announceErr
	}
	r.announced[vipAddress] = true
	return nil
}

func (r *recordingRouter) WithdrawVIP(_ context.Context, vipAddress string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.withdraws++
	r.events.record("withdraw:" + vipAddress)
	if r.withdrawErr != nil {
		return r.withdrawErr
	}
	delete(r.announced, vipAddress)
	return nil
}

func (r *recordingRouter) IsAnnounced(vipAddress string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.announced[vipAddress]
}

func (r *recordingRouter) Close() error {
	return nil
}

func (r *recordingRouter) announceCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.announces
}

func (r *recordingRouter) withdrawCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.withdraws
}

type recordingStatusUpdater struct {
	mu         sync.Mutex
	updates    int
	conditions []metav1.Condition
	events     *eventRecorder
}

func (r *recordingStatusUpdater) UpdateVIPStatus(_ context.Context, _ *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec, conditions ...metav1.Condition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates++
	r.conditions = append([]metav1.Condition(nil), conditions...)
	r.events.record("status")
	return nil
}

func (r *recordingStatusUpdater) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates
}

func (r *recordingStatusUpdater) lastConditions() []metav1.Condition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]metav1.Condition(nil), r.conditions...)
}

type recordingRolloutCoordinator struct {
	mu    sync.Mutex
	keys  []string
	calls int
}

func (r *recordingRolloutCoordinator) RunExclusive(ctx context.Context, key string, fn func(context.Context) error) error {
	r.mu.Lock()
	r.calls++
	r.keys = append(r.keys, key)
	r.mu.Unlock()
	return fn(ctx)
}

func (r *recordingRolloutCoordinator) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRolloutCoordinator) lastKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return ""
	}
	return r.keys[len(r.keys)-1]
}

func (r *recordingRolloutCoordinator) keysSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

type blockingRolloutCoordinator struct {
	recordingRolloutCoordinator

	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newBlockingRolloutCoordinator() *blockingRolloutCoordinator {
	return &blockingRolloutCoordinator{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingRolloutCoordinator) RunExclusive(ctx context.Context, key string, fn func(context.Context) error) error {
	r.mu.Lock()
	r.calls++
	r.keys = append(r.keys, key)
	call := r.calls
	r.mu.Unlock()

	if call == 1 {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fn(ctx)
}

func (r *blockingRolloutCoordinator) waitForFirstCall(t *testing.T) {
	t.Helper()
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rollout lock call")
	}
}

func (r *blockingRolloutCoordinator) releaseFirst() {
	close(r.release)
}
