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
			Name:      "web",
			Namespace: "default",
		},
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
