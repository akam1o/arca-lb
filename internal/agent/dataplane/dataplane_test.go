package dataplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"go.fd.io/govpp/binapi/ip_types"
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

func testVPPConfig() VPPConfig {
	return VPPConfig{
		EncapType:                 "L3DSR",
		DSCP:                      10,
		ServiceType:               "CLUSTERIP",
		NewFlowsTableLength:       65536,
		StateVerificationInterval: 30 * time.Second,
	}
}

func vppDetailForTest(t *testing.T, vpp *VPP, vip *v1alpha1.VirtualIP) *lb.LbVipDetails {
	t.Helper()
	attrs, err := vpp.effectiveVIPAttributes(vip)
	if err != nil {
		t.Fatalf("effectiveVIPAttributes: %v", err)
	}
	return &lb.LbVipDetails{
		Encap:           encapToAPI(attrs.encapType),
		Dscp:            ip_types.IPDscp(attrs.dscp),
		SrvType:         serviceTypeToAPI(attrs.serviceType),
		TargetPort:      uint16(attrs.port),
		FlowTableLength: uint16(attrs.newFlowsTableLength),
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
			NewFlowsTableLength: 65536,
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

	nonL3DSR := base.DeepCopy()
	nonL3DSR.Spec.EncapType = v1alpha1.EncapTypeNAT4
	nonL3DSRWithDSCP := nonL3DSR.DeepCopy()
	ignoredDSCP := uint8(11)
	nonL3DSRWithDSCP.Spec.DSCP = &ignoredDSCP
	if !vpp.sameVIPAttributes(nonL3DSR, nonL3DSRWithDSCP) {
		t.Fatal("non-L3DSR DSCP change should not require VIP recreation")
	}

	attrs, err := vpp.effectiveVIPAttributes(nonL3DSRWithDSCP)
	if err != nil {
		t.Fatalf("effectiveVIPAttributes: %v", err)
	}
	if attrs.dscp != 0 {
		t.Fatalf("non-L3DSR effective DSCP = %d, want 0", attrs.dscp)
	}

	detail := vppDetailForTest(t, vpp, nonL3DSRWithDSCP)
	detail.Dscp = ip_types.IPDscp(vpp.config.DSCP)
	if !vpp.vipDetailsMatchDesired(nonL3DSRWithDSCP, detail) {
		t.Fatal("non-L3DSR retained VIP should match regardless of dump DSCP")
	}
}

func TestVPPApplyVIPRejectsInvalidDesiredBeforeDeletingExisting(t *testing.T) {
	vpp := &VPP{
		config: VPPConfig{
			EncapType:           "GRE4",
			DSCP:                0,
			ServiceType:         "CLUSTERIP",
			NewFlowsTableLength: 65536,
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

func TestVPPApplyVIPSkipsStateVerificationBeforeInterval(t *testing.T) {
	now := time.Unix(100, 0)
	vpp := &VPP{
		config: testVPPConfig(),
		logger: discardLogger(),
		vips:   make(map[string]*vipEntry),
		now: func() time.Time {
			return now
		},
		lookupVIPFn: func(context.Context, *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error) {
			t.Fatal("lookupVIP should not be called before verification interval")
			return nil, false, nil
		},
		addVIPFn: func(context.Context, *v1alpha1.VirtualIP) error {
			t.Fatal("addVIP should not be called before verification interval")
			return nil
		},
		addBackendFn: func(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error {
			t.Fatal("addBackend should not be called when cached backends match")
			return nil
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	lastVerified := now.Add(-10 * time.Second)
	vpp.vips[key] = &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": vip.Spec.Backends[0],
			"10.0.1.2": vip.Spec.Backends[1],
		},
		lastVerified: lastVerified,
	}

	if err := vpp.ApplyVIP(context.Background(), vip, vip.Spec.Backends); err != nil {
		t.Fatalf("ApplyVIP: %v", err)
	}
	if got := vpp.vips[key].lastVerified; !got.Equal(lastVerified) {
		t.Fatalf("lastVerified = %v, want unchanged %v", got, lastVerified)
	}
}

func TestVPPApplyVIPRecreatesMissingCachedVIPAfterVerificationInterval(t *testing.T) {
	now := time.Unix(100, 0)
	lookupCalls := 0
	addVIPCalls := 0
	var added []string
	vpp := &VPP{
		config: testVPPConfig(),
		logger: discardLogger(),
		vips:   make(map[string]*vipEntry),
		now: func() time.Time {
			return now
		},
		lookupVIPFn: func(context.Context, *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error) {
			lookupCalls++
			return nil, false, nil
		},
		addVIPFn: func(context.Context, *v1alpha1.VirtualIP) error {
			addVIPCalls++
			return nil
		},
		addBackendFn: func(_ context.Context, _ *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
			added = append(added, be.Address)
			return nil
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	vpp.vips[key] = &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": vip.Spec.Backends[0],
			"10.0.1.2": vip.Spec.Backends[1],
		},
		lastVerified: now.Add(-31 * time.Second),
	}

	if err := vpp.ApplyVIP(context.Background(), vip, vip.Spec.Backends); err != nil {
		t.Fatalf("ApplyVIP: %v", err)
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls = %d, want 1", lookupCalls)
	}
	if addVIPCalls != 1 {
		t.Fatalf("add VIP calls = %d, want 1", addVIPCalls)
	}
	if len(added) != 2 {
		t.Fatalf("added backends = %v, want two backends", added)
	}
	if got := vpp.vips[key].lastVerified; !got.Equal(now) {
		t.Fatalf("lastVerified = %v, want %v", got, now)
	}
}

func TestVPPApplyVIPRefreshesCachedBackendsFromVPP(t *testing.T) {
	now := time.Unix(100, 0)
	var added []string
	dumpCalls := 0
	vpp := &VPP{
		config: testVPPConfig(),
		logger: discardLogger(),
		vips:   make(map[string]*vipEntry),
		now: func() time.Time {
			return now
		},
		dumpBackendsFn: func(_ context.Context, _ *v1alpha1.VirtualIP, _ []v1alpha1.BackendSpec) (map[string]v1alpha1.BackendSpec, error) {
			dumpCalls++
			return map[string]v1alpha1.BackendSpec{
				"10.0.1.1": {Address: "10.0.1.1", Weight: 100},
			}, nil
		},
		addBackendFn: func(_ context.Context, _ *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
			added = append(added, be.Address)
			return nil
		},
		addVIPFn: func(context.Context, *v1alpha1.VirtualIP) error {
			t.Fatal("addVIP should not be called when VPP VIP exists")
			return nil
		},
	}
	vpp.lookupVIPFn = func(_ context.Context, vip *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error) {
		return vppDetailForTest(t, vpp, vip), true, nil
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(vip)
	vpp.vips[key] = &vipEntry{
		vip: vip.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{
			"10.0.1.1": vip.Spec.Backends[0],
			"10.0.1.2": vip.Spec.Backends[1],
		},
		lastVerified: now.Add(-31 * time.Second),
	}

	if err := vpp.ApplyVIP(context.Background(), vip, vip.Spec.Backends); err != nil {
		t.Fatalf("ApplyVIP: %v", err)
	}
	if dumpCalls != 1 {
		t.Fatalf("dump calls = %d, want 1", dumpCalls)
	}
	if len(added) != 1 || added[0] != "10.0.1.2" {
		t.Fatalf("added backends = %v, want [10.0.1.2]", added)
	}
	if got := len(vpp.vips[key].backends); got != 2 {
		t.Fatalf("cached backend count = %d, want 2 after reconcile", got)
	}
	if got := vpp.vips[key].lastVerified; !got.Equal(now) {
		t.Fatalf("lastVerified = %v, want %v", got, now)
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

func TestVPPRecreateVIPMarksRouteSafeWhenDeleteFailsAndVIPIsRetained(t *testing.T) {
	deleteErr := errors.New("delete failed")
	var deleted *v1alpha1.VirtualIP
	inspected := false
	vpp := &VPP{
		vips:         make(map[string]*vipEntry),
		tuningDrifts: map[string][]VIPTuningDrift{},
		deleteVIPFn: func(_ context.Context, vip *v1alpha1.VirtualIP) error {
			deleted = vip.DeepCopy()
			return deleteErr
		},
		vipExistsFn: func(_ context.Context, _ *v1alpha1.VirtualIP) (bool, error) {
			inspected = true
			return true, nil
		},
	}

	applied := newTestVIP("test-vip", "203.0.113.1", 80)
	key := vpp.vipKey(applied)
	vpp.vips[key] = &vipEntry{
		vip:      applied.DeepCopy(),
		backends: map[string]v1alpha1.BackendSpec{},
	}
	vpp.tuningDrifts[key] = []VIPTuningDrift{{
		Field:   "new_flows_table_length",
		Current: "1024",
		Desired: "2048",
	}}

	err := vpp.RecreateVIP(context.Background(), applied.DeepCopy(), nil)
	if err == nil {
		t.Fatal("expected recreate delete failure")
	}
	if !errors.Is(err, deleteErr) {
		t.Fatalf("RecreateVIP error = %v, want wrapped delete error", err)
	}
	if !RouteSafeToRestoreAfterVIPRecreate(err) {
		t.Fatal("delete failure with retained VIP should be route-safe to restore")
	}
	var recreateErr *VIPRecreateError
	if !errors.As(err, &recreateErr) {
		t.Fatalf("RecreateVIP error = %T, want VIPRecreateError", err)
	}
	if recreateErr.Stage != VIPRecreateStageDelete {
		t.Fatalf("recreate stage = %s, want delete", recreateErr.Stage)
	}
	if deleted == nil || deleted.Spec.Port != applied.Spec.Port {
		t.Fatalf("deleted VIP = %+v, want tracked applied VIP", deleted)
	}
	if !inspected {
		t.Fatal("retained VIP was not inspected after delete failure")
	}
	if _, ok := vpp.vips[key]; !ok {
		t.Fatal("tracked VIP should remain after delete failure")
	}
	if drifts := vpp.TuningDrifts(key); len(drifts) != 1 {
		t.Fatalf("tuning drifts = %+v, want retained drift for retry", drifts)
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
			NewFlowsTableLength: 65536,
		},
	}

	vip := newTestVIP("test-vip", "203.0.113.1", 80)
	if drifts := vpp.detectTuningDrifts(vip, &lb.LbVipDetails{FlowTableLength: 0}); len(drifts) != 0 {
		t.Fatalf("drift count = %d, want 0 when dump-width representation matches", len(drifts))
	}
	if drifts := vpp.detectTuningDrifts(vip, &lb.LbVipDetails{FlowTableLength: 1}); len(drifts) != 1 {
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
