package dataplane

import (
	"context"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestVIP(name, addr string, port int) *v1alpha1.VirtualIP {
	return &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  addr,
			Port:     port,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.1.1", Weight: 100},
				{Address: "10.0.1.2", Weight: 100},
			},
		},
	}
}

func TestNoopApplyAndRemoveVIP(t *testing.T) {
	dp, err := New("noop", nil)
	if err != nil {
		t.Fatalf("New noop: %v", err)
	}
	defer func() { _ = dp.Close() }()

	ctx := context.Background()
	vip := newTestVIP("test-vip", "203.0.113.1", 80)

	// Apply
	if err := dp.ApplyVIP(ctx, vip, vip.Spec.Backends); err != nil {
		t.Fatalf("ApplyVIP: %v", err)
	}

	// Check state
	state, err := dp.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.VIPs) != 1 {
		t.Fatalf("expected 1 VIP, got %d", len(state.VIPs))
	}
	if state.VIPs[0].Address != "203.0.113.1" {
		t.Errorf("VIP address = %q, want 203.0.113.1", state.VIPs[0].Address)
	}
	if len(state.VIPs[0].Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(state.VIPs[0].Backends))
	}

	// Remove
	if err := dp.RemoveVIP(ctx, vip); err != nil {
		t.Fatalf("RemoveVIP: %v", err)
	}

	state, err = dp.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState after remove: %v", err)
	}
	if len(state.VIPs) != 0 {
		t.Fatalf("expected 0 VIPs after remove, got %d", len(state.VIPs))
	}
}

func TestNoopSetBackends(t *testing.T) {
	dp, err := New("noop", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dp.Close() }()

	ctx := context.Background()
	vip := newTestVIP("test-vip", "203.0.113.1", 80)

	if err := dp.ApplyVIP(ctx, vip, vip.Spec.Backends); err != nil {
		t.Fatal(err)
	}

	// Set to 1 backend
	newBackends := []v1alpha1.BackendSpec{{Address: "10.0.1.3", Weight: 50}}
	if err := dp.SetBackends(ctx, vip, newBackends); err != nil {
		t.Fatalf("SetBackends: %v", err)
	}

	state, _ := dp.GetState(ctx)
	if len(state.VIPs[0].Backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(state.VIPs[0].Backends))
	}
	if state.VIPs[0].Backends[0].Address != "10.0.1.3" {
		t.Errorf("backend address = %q, want 10.0.1.3", state.VIPs[0].Backends[0].Address)
	}
}

func TestNoopAddRemoveBackend(t *testing.T) {
	dp, err := New("noop", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dp.Close() }()

	ctx := context.Background()
	vip := newTestVIP("test-vip", "203.0.113.1", 80)

	if err := dp.ApplyVIP(ctx, vip, nil); err != nil {
		t.Fatal(err)
	}

	be := v1alpha1.BackendSpec{Address: "10.0.1.1", Weight: 100}
	if err := dp.AddBackend(ctx, vip, be); err != nil {
		t.Fatalf("AddBackend: %v", err)
	}

	state, _ := dp.GetState(ctx)
	if len(state.VIPs[0].Backends) != 1 {
		t.Errorf("expected 1 backend after add, got %d", len(state.VIPs[0].Backends))
	}

	if err := dp.RemoveBackend(ctx, vip, be); err != nil {
		t.Fatalf("RemoveBackend: %v", err)
	}

	state, _ = dp.GetState(ctx)
	if len(state.VIPs[0].Backends) != 0 {
		t.Errorf("expected 0 backends after remove, got %d", len(state.VIPs[0].Backends))
	}
}

func TestNewUnsupportedType(t *testing.T) {
	_, err := New("invalid", nil)
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestNoopRemoveNonexistent(t *testing.T) {
	dp, err := New("noop", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dp.Close() }()

	ctx := context.Background()
	vip := newTestVIP("test-vip", "203.0.113.1", 80)

	// Should not error
	if err := dp.RemoveVIP(ctx, vip); err != nil {
		t.Fatalf("RemoveVIP for non-existent: %v", err)
	}
}
