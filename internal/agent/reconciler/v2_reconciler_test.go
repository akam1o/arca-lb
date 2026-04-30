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

func TestV2ReconcilerSkipsStatusAndRouteWhenDataPlaneFails(t *testing.T) {
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
	if got := statusUpdater.updateCount(); got != 0 {
		t.Fatalf("status updates = %d, want 0 after dataplane apply failure", got)
	}
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

type recordingDataPlane struct {
	mu       sync.Mutex
	applies  int
	removals int
	applyErr error
}

func newRecordingDataPlane() *recordingDataPlane {
	return &recordingDataPlane{}
}

func (r *recordingDataPlane) ApplyVIP(_ context.Context, _ *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applies++
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

func (r *recordingDataPlane) applyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applies
}

func (r *recordingDataPlane) removeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removals
}

type recordingStatusUpdater struct {
	mu      sync.Mutex
	updates int
}

func (r *recordingStatusUpdater) UpdateVIPStatus(_ context.Context, _ *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates++
	return nil
}

func (r *recordingStatusUpdater) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates
}
