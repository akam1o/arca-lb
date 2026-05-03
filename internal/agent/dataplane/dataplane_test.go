package dataplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"go.fd.io/govpp/binapi/lb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

func TestVPPSameVIPAttributes(t *testing.T) {
	vpp := &VPP{
		config: VPPConfig{
			EncapType:           "L3DSR",
			DSCP:                10,
			ServiceType:         "CLUSTERIP",
			NewFlowsTableLength: 65537,
		},
	}

	base := newTestVIP("test-vip", "203.0.113.1", 80)
	same := base.DeepCopy()
	same.Spec.Backends = []v1alpha1.BackendSpec{{Address: "10.0.1.3", Weight: 50}}
	if !vpp.sameVIPAttributes(base, same) {
		t.Fatal("backend-only change should not require VIP recreation")
	}

	explicitDefaultDSCP := base.DeepCopy()
	defaultDSCP := uint8(10)
	explicitDefaultDSCP.Spec.DSCP = &defaultDSCP
	if !vpp.sameVIPAttributes(base, explicitDefaultDSCP) {
		t.Fatal("nil DSCP and explicit default DSCP should be treated as the same VPP VIP")
	}

	portChanged := base.DeepCopy()
	portChanged.Spec.Port = 443
	if vpp.sameVIPAttributes(base, portChanged) {
		t.Fatal("port change should require VIP recreation")
	}

	dscpChanged := base.DeepCopy()
	newDSCP := uint8(11)
	dscpChanged.Spec.DSCP = &newDSCP
	if vpp.sameVIPAttributes(base, dscpChanged) {
		t.Fatal("effective L3DSR DSCP change should require VIP recreation")
	}

	encapChanged := base.DeepCopy()
	encapChanged.Spec.EncapType = v1alpha1.EncapTypeNAT4
	if vpp.sameVIPAttributes(base, encapChanged) {
		t.Fatal("encapType change should require VIP recreation")
	}
}

func TestVPPApplyVIPRejectsInvalidDesiredBeforeDeletingExisting(t *testing.T) {
	vpp := &VPP{
		config: VPPConfig{
			EncapType:           "GRE4",
			DSCP:                0,
			ServiceType:         "CLUSTERIP",
			NewFlowsTableLength: 65537,
		},
		vips: make(map[string]*vipEntry),
	}

	existing := newTestVIP("test-vip", "203.0.113.1", 80)
	existing.Spec.EncapType = v1alpha1.EncapTypeL3DSR
	existingDSCP := uint8(10)
	existing.Spec.DSCP = &existingDSCP
	key := vpp.vipKey(existing)
	vpp.vips[key] = &vipEntry{
		vip:      existing.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{},
	}

	invalidDesired := existing.DeepCopy()
	invalidDesired.Spec.DSCP = nil

	err := vpp.ApplyVIP(context.Background(), invalidDesired, nil)
	if err == nil {
		t.Fatal("expected invalid desired VIP to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid desired VIP attributes") {
		t.Fatalf("ApplyVIP error = %q, want invalid desired VIP attributes", err)
	}

	entry, ok := vpp.vips[key]
	if !ok {
		t.Fatal("existing VIP entry was removed after invalid desired update")
	}
	if entry.vip.Spec.DSCP == nil || *entry.vip.Spec.DSCP != existingDSCP {
		t.Fatalf("existing VIP was changed: dscp=%v", entry.vip.Spec.DSCP)
	}
}

func TestVPPRemoveVIPUsesTrackedAppliedSpec(t *testing.T) {
	var deleted *v1alpha1.VirtualIP
	vpp := &VPP{
		vips: make(map[string]*vipEntry),
		deleteVIPFn: func(_ context.Context, vip *v1alpha1.VirtualIP) error {
			deleted = vip.DeepCopy()
			return nil
		},
	}

	applied := newTestVIP("test-vip", "203.0.113.1", 80)
	applied.Spec.EncapType = v1alpha1.EncapTypeL3DSR
	appliedDSCP := uint8(10)
	applied.Spec.DSCP = &appliedDSCP
	key := vpp.vipKey(applied)
	vpp.vips[key] = &vipEntry{
		vip:      applied.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{},
	}

	deleteEvent := applied.DeepCopy()
	deleteEvent.Spec.Port = 443
	deleteEvent.Spec.EncapType = v1alpha1.EncapTypeNAT4
	deleteEvent.Spec.DSCP = nil

	if err := vpp.RemoveVIP(context.Background(), deleteEvent); err != nil {
		t.Fatalf("RemoveVIP: %v", err)
	}
	if deleted == nil {
		t.Fatal("deleteVIP was not called")
	}
	if deleted.Spec.Port != applied.Spec.Port {
		t.Fatalf("deleted port = %d, want applied port %d", deleted.Spec.Port, applied.Spec.Port)
	}
	if deleted.Spec.EncapType != applied.Spec.EncapType {
		t.Fatalf("deleted encapType = %s, want applied encapType %s", deleted.Spec.EncapType, applied.Spec.EncapType)
	}
	if deleted.Spec.DSCP == nil || *deleted.Spec.DSCP != appliedDSCP {
		t.Fatalf("deleted DSCP = %v, want applied DSCP %d", deleted.Spec.DSCP, appliedDSCP)
	}
	if _, ok := vpp.vips[key]; ok {
		t.Fatal("tracked VIP entry was not removed")
	}
}

func TestVPPDetectsFlowTableLengthTuningDrift(t *testing.T) {
	vpp := &VPP{
		config: VPPConfig{
			EncapType:           "L3DSR",
			DSCP:                10,
			ServiceType:         "CLUSTERIP",
			NewFlowsTableLength: 2048,
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	drifts := vpp.detectTuningDrifts(vip, &lb.LbVipDetails{FlowTableLength: 1024})
	if len(drifts) != 1 {
		t.Fatalf("drift count = %d, want 1", len(drifts))
	}
	if drifts[0].Field != "new_flows_table_length" || drifts[0].Current != "1024" || drifts[0].Desired != "2048" {
		t.Fatalf("drift = %#v, want new_flows_table_length 1024 -> 2048", drifts[0])
	}
}

func TestVPPDetectTuningDriftsUsesDumpWidthForFlowTableLength(t *testing.T) {
	vpp := &VPP{
		config: VPPConfig{
			EncapType:           "L3DSR",
			DSCP:                10,
			ServiceType:         "CLUSTERIP",
			NewFlowsTableLength: 65537,
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	if drifts := vpp.detectTuningDrifts(vip, &lb.LbVipDetails{FlowTableLength: 1}); len(drifts) != 0 {
		t.Fatalf("drift count = %d, want 0 when dump-width representation matches", len(drifts))
	}
	if drifts := vpp.detectTuningDrifts(vip, &lb.LbVipDetails{FlowTableLength: 2}); len(drifts) != 1 {
		t.Fatalf("drift count = %d, want 1 when dump-width representation differs", len(drifts))
	}
}

func TestVPPReconcileBackendsReturnsRemoveError(t *testing.T) {
	removeErr := errors.New("remove failed")
	vpp := &VPP{
		logger: discardLogger(),
		removeBackendFn: func(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error {
			return removeErr
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	entry := &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": {Address: "10.0.1.1", Weight: 100},
			"10.0.1.2": {Address: "10.0.1.2", Weight: 100},
		},
	}

	err := vpp.reconcileBackendsLocked(
		context.Background(),
		key,
		entry,
		vip,
		[]v1alpha1.BackendSpec{{Address: "10.0.1.2", Weight: 100}},
	)
	if err == nil {
		t.Fatal("expected remove backend failure to be returned")
	}
	if !errors.Is(err, removeErr) {
		t.Fatalf("reconcileBackendsLocked error = %v, want wrapped remove error", err)
	}
	if !strings.Contains(err.Error(), "failed to reconcile backend set") {
		t.Fatalf("reconcileBackendsLocked error = %q, want backend set context", err)
	}
	if _, ok := entry.backends["10.0.1.1"]; !ok {
		t.Fatal("failed remove should leave stale backend tracked")
	}
}

func TestVPPReconcileBackendsReturnsPartialAddError(t *testing.T) {
	addErr := errors.New("add failed")
	vpp := &VPP{
		logger: discardLogger(),
		addBackendFn: func(_ context.Context, _ *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
			if be.Address == "10.0.1.3" {
				return addErr
			}
			return nil
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	entry := &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": {Address: "10.0.1.1", Weight: 100},
		},
	}

	err := vpp.reconcileBackendsLocked(
		context.Background(),
		key,
		entry,
		vip,
		[]v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 100},
			{Address: "10.0.1.3", Weight: 100},
		},
	)
	if err == nil {
		t.Fatal("expected partial add backend failure to be returned")
	}
	if !errors.Is(err, addErr) {
		t.Fatalf("reconcileBackendsLocked error = %v, want wrapped add error", err)
	}
	if _, ok := entry.backends["10.0.1.3"]; ok {
		t.Fatal("failed add should not be tracked as applied")
	}
}

func TestVPPReconcileBackendsStoresUnequalWeightsAsMetadata(t *testing.T) {
	var added []v1alpha1.BackendSpec
	vpp := &VPP{
		logger: discardLogger(),
		addBackendFn: func(_ context.Context, _ *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
			added = append(added, be)
			return nil
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	entry := &vipEntry{
		vip:      vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{},
	}

	err := vpp.reconcileBackendsLocked(
		context.Background(),
		key,
		entry,
		vip,
		[]v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 10},
			{Address: "10.0.1.2", Weight: 20},
		},
	)
	if err != nil {
		t.Fatalf("reconcileBackendsLocked rejected unequal weights: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("added backend count = %d, want 2", len(added))
	}
	if entry.backends["10.0.1.1"].Weight != 10 {
		t.Fatalf("backend 10.0.1.1 weight = %d, want 10", entry.backends["10.0.1.1"].Weight)
	}
	if entry.backends["10.0.1.2"].Weight != 20 {
		t.Fatalf("backend 10.0.1.2 weight = %d, want 20", entry.backends["10.0.1.2"].Weight)
	}
}

func TestVPPReconcileBackendsAllowsEqualWeightMetadataUpdate(t *testing.T) {
	vpp := &VPP{
		logger: discardLogger(),
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	entry := &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": {Address: "10.0.1.1", Weight: 100},
			"10.0.1.2": {Address: "10.0.1.2", Weight: 100},
		},
	}

	err := vpp.reconcileBackendsLocked(
		context.Background(),
		key,
		entry,
		vip,
		[]v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 50},
			{Address: "10.0.1.2", Weight: 50},
		},
	)
	if err != nil {
		t.Fatalf("reconcileBackendsLocked rejected equal weights: %v", err)
	}
	for addr, be := range entry.backends {
		if be.Weight != 50 {
			t.Fatalf("backend %s weight = %d, want 50", addr, be.Weight)
		}
	}
}
