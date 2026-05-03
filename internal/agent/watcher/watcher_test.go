package watcher

import (
	"io"
	"log/slog"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8scache "k8s.io/client-go/tools/cache"
)

type recordingHandler struct {
	updated []*v1alpha1.VirtualIP
	deleted []*v1alpha1.VirtualIP
}

func (h *recordingHandler) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	h.updated = append(h.updated, vip)
}

func (h *recordingHandler) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	h.deleted = append(h.deleted, vip)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newWatcherTestVIP() *v1alpha1.VirtualIP {
	return &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
		},
	}
}

func TestEventHandlerOnUpdateIgnoresStatusOnlyUpdate(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	oldVIP := newWatcherTestVIP()
	newVIP := oldVIP.DeepCopy()
	newVIP.Status = v1alpha1.VirtualIPStatus{
		HealthyBackends: 1,
		TotalBackends:   1,
	}

	eh.OnUpdate(oldVIP, newVIP)

	if len(handler.updated) != 0 {
		t.Fatalf("expected no update for status-only change, got %d", len(handler.updated))
	}
}

func TestEventHandlerOnUpdateHandlesSpecChange(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	oldVIP := newWatcherTestVIP()
	newVIP := oldVIP.DeepCopy()
	newVIP.Spec.Port = 443

	eh.OnUpdate(oldVIP, newVIP)

	if len(handler.updated) != 1 {
		t.Fatalf("expected 1 updated VIP, got %d", len(handler.updated))
	}
	if handler.updated[0] != newVIP {
		t.Fatalf("updated VIP = %#v, want new VIP", handler.updated[0])
	}
}

func TestEventHandlerOnUpdateHandlesGenerationChange(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	oldVIP := newWatcherTestVIP()
	newVIP := oldVIP.DeepCopy()
	newVIP.Generation = oldVIP.Generation + 1

	eh.OnUpdate(oldVIP, newVIP)

	if len(handler.updated) != 1 {
		t.Fatalf("expected 1 updated VIP, got %d", len(handler.updated))
	}
}

func TestInitialSyncEventGateBuffersUntilRelease(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	gate := newInitialSyncEventGate(eh)
	addedVIP := newWatcherTestVIP()
	updatedVIP := addedVIP.DeepCopy()
	updatedVIP.Spec.Port = 443

	gate.OnAdd(addedVIP, true)
	gate.OnUpdate(addedVIP, updatedVIP)

	if len(handler.updated) != 0 {
		t.Fatalf("expected no updates before release, got %d", len(handler.updated))
	}

	gate.Release()

	if len(handler.updated) != 2 {
		t.Fatalf("expected 2 updates after release, got %d", len(handler.updated))
	}
	if handler.updated[0] != addedVIP {
		t.Fatalf("first released VIP = %#v, want added VIP", handler.updated[0])
	}
	if handler.updated[1] != updatedVIP {
		t.Fatalf("second released VIP = %#v, want updated VIP", handler.updated[1])
	}
}

func TestInitialSyncEventGateDeliversAfterRelease(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	gate := newInitialSyncEventGate(eh)
	vip := newWatcherTestVIP()

	gate.Release()
	gate.OnAdd(vip, false)

	if len(handler.updated) != 1 {
		t.Fatalf("expected 1 update after release, got %d", len(handler.updated))
	}
	if handler.updated[0] != vip {
		t.Fatalf("updated VIP = %#v, want VIP", handler.updated[0])
	}
}

func TestEventHandlerOnDeleteHandlesVirtualIP(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	vip := newWatcherTestVIP()

	eh.OnDelete(vip)

	if len(handler.deleted) != 1 {
		t.Fatalf("expected 1 deleted VIP, got %d", len(handler.deleted))
	}
	if handler.deleted[0] != vip {
		t.Fatalf("deleted VIP = %#v, want original VIP", handler.deleted[0])
	}
}

func TestEventHandlerOnDeleteHandlesTombstone(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}
	vip := newWatcherTestVIP()

	eh.OnDelete(k8scache.DeletedFinalStateUnknown{Obj: vip})

	if len(handler.deleted) != 1 {
		t.Fatalf("expected 1 deleted VIP, got %d", len(handler.deleted))
	}
	if handler.deleted[0] != vip {
		t.Fatalf("deleted VIP = %#v, want tombstone VIP", handler.deleted[0])
	}
}

func TestEventHandlerOnDeleteIgnoresInvalidTombstone(t *testing.T) {
	handler := &recordingHandler{}
	eh := &eventHandler{handler: handler, logger: newTestLogger()}

	eh.OnDelete(k8scache.DeletedFinalStateUnknown{Obj: "not-a-virtualip"})

	if len(handler.deleted) != 0 {
		t.Fatalf("expected no deleted VIPs, got %d", len(handler.deleted))
	}
}
