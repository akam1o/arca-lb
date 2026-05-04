package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"go.fd.io/govpp"
	"go.fd.io/govpp/api"
	"go.fd.io/govpp/binapi/ip_types"
	"go.fd.io/govpp/binapi/lb"
	"go.fd.io/govpp/binapi/lb_types"
	"go.fd.io/govpp/core"
)

// VPPConfig holds VPP-specific configuration.
type VPPConfig struct {
	SocketPath                string
	ConnectTimeout            time.Duration
	ReconnectInterval         time.Duration
	EncapType                 string
	DSCP                      uint8
	ServiceType               string
	NewFlowsTableLength       uint32
	FailOnAllBackendsDown     bool
	StateVerificationInterval time.Duration
}

func vppConfigFromMap(m map[string]interface{}) VPPConfig {
	cfg := VPPConfig{
		SocketPath:                "/run/vpp/api.sock",
		ConnectTimeout:            10 * time.Second,
		ReconnectInterval:         5 * time.Second,
		EncapType:                 "L3DSR",
		DSCP:                      10,
		ServiceType:               "CLUSTERIP",
		NewFlowsTableLength:       65536,
		StateVerificationInterval: 30 * time.Second,
	}
	if m == nil {
		return cfg
	}
	if v, ok := m["socket_path"].(string); ok {
		cfg.SocketPath = v
	}
	if v, ok := m["encap_type"].(string); ok {
		cfg.EncapType = v
	}
	if v, ok := m["dscp"].(int); ok {
		cfg.DSCP = uint8(v)
	}
	if v, ok := m["service_type"].(string); ok {
		cfg.ServiceType = v
	}
	if v, ok := m["new_flows_table_length"].(int); ok {
		cfg.NewFlowsTableLength = uint32(v)
	}
	if v, ok := vppDurationSetting(m["state_verification_interval"]); ok {
		cfg.StateVerificationInterval = v
	}
	return cfg
}

func vppDurationSetting(value interface{}) (time.Duration, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case time.Duration:
		return v, true
	case string:
		if v == "" {
			return 0, false
		}
		d, err := time.ParseDuration(v)
		return d, err == nil
	case int:
		return time.Duration(v) * time.Second, true
	case int64:
		return time.Duration(v) * time.Second, true
	case float64:
		return time.Duration(v * float64(time.Second)), true
	default:
		return 0, false
	}
}

// VPP implements DataPlane using fd.io VPP.
type VPP struct {
	config VPPConfig
	conn   *core.Connection
	logger *slog.Logger

	addBackendFn    func(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error
	removeBackendFn func(context.Context, *v1alpha1.VirtualIP, v1alpha1.BackendSpec) error
	addVIPFn        func(context.Context, *v1alpha1.VirtualIP) error
	deleteVIPFn     func(context.Context, *v1alpha1.VirtualIP) error
	vipExistsFn     func(context.Context, *v1alpha1.VirtualIP) (bool, error)
	lookupVIPFn     func(context.Context, *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error)
	dumpBackendsFn  func(context.Context, *v1alpha1.VirtualIP, []v1alpha1.BackendSpec) (map[string]v1alpha1.BackendSpec, error)
	now             func() time.Time

	mu   sync.RWMutex
	vips map[string]*vipEntry // key: namespace/name

	tuningDrifts map[string][]VIPTuningDrift // key: namespace/name
}

type vipEntry struct {
	vip          *v1alpha1.VirtualIP
	backends     map[string]v1alpha1.BackendSpec // key: address
	lastVerified time.Time
}

type vipAttributes struct {
	address             string
	port                int
	protocol            v1alpha1.Protocol
	encapType           string
	dscp                uint8
	serviceType         string
	newFlowsTableLength uint32
}

// NewVPP creates a new VPP data plane.
func NewVPP(cfg map[string]interface{}) (*VPP, error) {
	config := vppConfigFromMap(cfg)

	conn, err := govpp.Connect(config.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to VPP at %s: %w", config.SocketPath, err)
	}

	return &VPP{
		config:       config,
		conn:         conn,
		logger:       slog.Default().With("component", "dataplane-vpp"),
		vips:         make(map[string]*vipEntry),
		tuningDrifts: make(map[string][]VIPTuningDrift),
	}, nil
}

func (v *VPP) vipKey(vip *v1alpha1.VirtualIP) string {
	return vip.Namespace + "/" + vip.Name
}

func (v *VPP) currentTime() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

func (v *VPP) stateVerificationInterval() time.Duration {
	if v.config.StateVerificationInterval > 0 {
		return v.config.StateVerificationInterval
	}
	return 30 * time.Second
}

func (v *VPP) shouldVerifyCachedVIP(entry *vipEntry, now time.Time) bool {
	if entry == nil {
		return false
	}
	return entry.lastVerified.IsZero() ||
		!now.Before(entry.lastVerified.Add(v.stateVerificationInterval()))
}

func (v *VPP) ApplyVIP(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	now := v.currentTime()
	verifiedNow := false
	verifiedMissing := false
	desiredAttrs, err := v.effectiveVIPAttributes(vip)
	if err != nil {
		return fmt.Errorf("invalid desired VIP attributes for %s: %w", key, err)
	}

	existing, exists := v.vips[key]
	if exists {
		existingAttrs, err := v.effectiveVIPAttributes(existing.vip)
		if err != nil {
			return fmt.Errorf("invalid existing VIP attributes for %s: %w", key, err)
		}
		if existingAttrs != desiredAttrs {
			if err := v.deleteCachedVIPForAttributeChangeLocked(ctx, key, existing); err != nil {
				return err
			}
			exists = false
			existing = nil
		} else if v.shouldVerifyCachedVIP(existing, now) {
			refreshed, found, err := v.refreshCachedVIPLocked(ctx, key, vip, healthyBackends, now)
			if err != nil {
				v.markVIPNeedsVerificationLocked(key)
				return fmt.Errorf("failed to verify cached VIP for %s: %w", key, err)
			}
			if found {
				existing = refreshed
				verifiedNow = true
			} else {
				exists = false
				existing = nil
				verifiedMissing = true
			}
		}
	}

	if !exists {
		adopted := false
		if !verifiedMissing {
			adopted, err = v.adoptExistingVIPLocked(ctx, vip, healthyBackends, now)
			if err != nil {
				return fmt.Errorf("failed to inspect existing VIP for %s: %w", key, err)
			}
		}
		if !adopted {
			if err := v.addVIP(ctx, vip); err != nil {
				return fmt.Errorf("failed to add VIP: %w", err)
			}

			existing = &vipEntry{
				vip:      vip.DeepCopy(),
				backends: make(map[string]v1alpha1.BackendSpec),
			}
			v.vips[key] = existing
			v.clearTuningDriftsLocked(key)
		} else {
			existing = v.vips[key]
		}
		verifiedNow = true
	}

	if err := v.reconcileBackendsLocked(ctx, key, existing, vip, healthyBackends); err != nil {
		v.markVIPNeedsVerificationLocked(key)
		return err
	}
	existing.vip = vip.DeepCopy()
	if verifiedNow {
		existing.lastVerified = now
	}

	return nil
}

func (v *VPP) RemoveVIP(ctx context.Context, vip *v1alpha1.VirtualIP) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	deleteTarget := vip
	if entry, ok := v.vips[key]; ok {
		deleteTarget = entry.vip
	} else {
		exists, err := v.vipExistsLocked(ctx, vip)
		if err != nil {
			return fmt.Errorf("failed to inspect VIP before remove: %w", err)
		}
		if !exists {
			v.clearTuningDriftsLocked(key)
			return nil // already removed
		}
	}

	if err := v.deleteVIP(ctx, deleteTarget); err != nil {
		return err
	}

	delete(v.vips, key)
	v.clearTuningDriftsLocked(key)
	return nil
}

func (v *VPP) deleteCachedVIPForAttributeChangeLocked(ctx context.Context, key string, entry *vipEntry) error {
	if entry == nil || entry.vip == nil {
		delete(v.vips, key)
		v.clearTuningDriftsLocked(key)
		return nil
	}

	_, exists, err := v.lookupVIP(ctx, entry.vip)
	if err != nil {
		return fmt.Errorf("failed to inspect existing VIP for update: %w", err)
	}
	if exists {
		if err := v.deleteVIP(ctx, entry.vip); err != nil {
			return fmt.Errorf("failed to delete existing VIP for update: %w", err)
		}
	}
	delete(v.vips, key)
	v.clearTuningDriftsLocked(key)
	return nil
}

func (v *VPP) refreshCachedVIPLocked(ctx context.Context, key string, vip *v1alpha1.VirtualIP, desiredBackends []v1alpha1.BackendSpec, now time.Time) (*vipEntry, bool, error) {
	detail, exists, err := v.lookupVIP(ctx, vip)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		delete(v.vips, key)
		v.clearTuningDriftsLocked(key)
		v.logger.Warn("cached VPP VIP is missing, recreating", "vip", key)
		return nil, false, nil
	}
	if !v.vipDetailsMatchDesired(vip, detail) {
		if err := v.deleteVIP(ctx, vip); err != nil {
			return nil, true, fmt.Errorf("failed to delete cached VIP with stale attributes: %w", err)
		}
		delete(v.vips, key)
		v.clearTuningDriftsLocked(key)
		return nil, false, nil
	}

	backends, err := v.dumpBackends(ctx, vip, desiredBackends)
	if err != nil {
		return nil, true, err
	}
	entry := &vipEntry{
		vip:          vip.DeepCopy(),
		backends:     backends,
		lastVerified: now,
	}
	v.vips[key] = entry
	v.setTuningDriftsLocked(key, v.detectTuningDrifts(vip, detail))
	return entry, true, nil
}

func (v *VPP) markVIPNeedsVerificationLocked(key string) {
	if entry := v.vips[key]; entry != nil {
		entry.lastVerified = time.Time{}
	}
}

func (v *VPP) adoptExistingVIPLocked(ctx context.Context, vip *v1alpha1.VirtualIP, desiredBackends []v1alpha1.BackendSpec, now time.Time) (bool, error) {
	detail, exists, err := v.lookupVIP(ctx, vip)
	if err != nil || !exists {
		return exists, err
	}
	if !v.vipDetailsMatchDesired(vip, detail) {
		if err := v.deleteVIP(ctx, vip); err != nil {
			return false, fmt.Errorf("failed to delete retained VIP with stale attributes: %w", err)
		}
		v.clearTuningDriftsLocked(v.vipKey(vip))
		return false, nil
	}

	backends, err := v.dumpBackends(ctx, vip, desiredBackends)
	if err != nil {
		return false, err
	}

	key := v.vipKey(vip)
	v.vips[key] = &vipEntry{
		vip:          vip.DeepCopy(),
		backends:     backends,
		lastVerified: now,
	}
	drifts := v.detectTuningDrifts(vip, detail)
	v.setTuningDriftsLocked(key, drifts)
	v.logger.Info("adopted retained VPP VIP", "vip", key, "backends", len(backends), "tuning_drifts", len(drifts))
	return true, nil
}

func (v *VPP) TuningDrifts(vipKey string) []VIPTuningDrift {
	v.mu.RLock()
	defer v.mu.RUnlock()

	drifts := v.tuningDrifts[vipKey]
	if len(drifts) == 0 {
		return nil
	}
	out := make([]VIPTuningDrift, len(drifts))
	copy(out, drifts)
	return out
}

func (v *VPP) NeedsDrainForVIPUpdate(current, desired *v1alpha1.VirtualIP) (bool, error) {
	if current == nil || desired == nil || current.Spec.Address != desired.Spec.Address {
		return false, nil
	}

	currentAttrs, err := v.effectiveVIPAttributes(current)
	if err != nil {
		return false, fmt.Errorf("invalid current VIP attributes for %s: %w", v.vipKey(current), err)
	}
	desiredAttrs, err := v.effectiveVIPAttributes(desired)
	if err != nil {
		return false, fmt.Errorf("invalid desired VIP attributes for %s: %w", v.vipKey(desired), err)
	}
	return currentAttrs != desiredAttrs, nil
}

func (v *VPP) NeedsDrainForRetainedVIP(ctx context.Context, vip *v1alpha1.VirtualIP) (bool, error) {
	if vip == nil {
		return false, nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	desiredAttrs, err := v.effectiveVIPAttributes(vip)
	if err != nil {
		return false, fmt.Errorf("invalid desired VIP attributes for %s: %w", key, err)
	}

	if entry := v.vips[key]; entry != nil {
		existingAttrs, err := v.effectiveVIPAttributes(entry.vip)
		if err != nil {
			return false, fmt.Errorf("invalid cached VIP attributes for %s: %w", key, err)
		}
		if existingAttrs != desiredAttrs {
			return true, nil
		}
	}

	detail, exists, err := v.lookupVIP(ctx, vip)
	if err != nil || !exists {
		if entry := v.vips[key]; entry != nil && !exists {
			entry.lastVerified = time.Time{}
		}
		return false, err
	}
	return !v.vipDetailsMatchDesired(vip, detail), nil
}

func (v *VPP) RecreateVIP(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	deleteTarget := vip
	if entry, ok := v.vips[key]; ok {
		deleteTarget = entry.vip
	}
	if err := v.deleteVIP(ctx, deleteTarget); err != nil {
		retained, inspectErr := v.vipExists(ctx, deleteTarget)
		if inspectErr != nil {
			retained = false
		}
		return &VIPRecreateError{
			Stage:              VIPRecreateStageDelete,
			RouteSafeToRestore: retained,
			Err:                fmt.Errorf("failed to delete VIP before recreate: %w", err),
		}
	}
	delete(v.vips, key)

	if err := v.addVIP(ctx, vip); err != nil {
		v.clearTuningDriftsLocked(key)
		return &VIPRecreateError{
			Stage: VIPRecreateStageAdd,
			Err:   fmt.Errorf("failed to add VIP during recreate: %w", err),
		}
	}

	entry := &vipEntry{
		vip:      vip.DeepCopy(),
		backends: make(map[string]v1alpha1.BackendSpec),
	}
	v.vips[key] = entry
	v.clearTuningDriftsLocked(key)
	if err := v.reconcileBackendsLocked(ctx, key, entry, vip, healthyBackends); err != nil {
		return &VIPRecreateError{
			Stage: VIPRecreateStageBackends,
			Err:   err,
		}
	}

	return nil
}

func (v *VPP) SetBackends(ctx context.Context, vip *v1alpha1.VirtualIP, backends []v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	entry, ok := v.vips[key]
	if !ok {
		return fmt.Errorf("VIP %s not found in data plane", key)
	}

	return v.reconcileBackendsLocked(ctx, key, entry, vip, backends)
}

func (v *VPP) reconcileBackendsLocked(
	ctx context.Context,
	key string,
	entry *vipEntry,
	vip *v1alpha1.VirtualIP,
	backends []v1alpha1.BackendSpec,
) error {
	desired := make(map[string]v1alpha1.BackendSpec)
	for _, be := range backends {
		desired[be.Address] = be
	}

	var firstErr error

	// Remove backends not in desired set
	for addr, be := range entry.backends {
		if _, ok := desired[addr]; !ok {
			if err := v.removeBackend(ctx, vip, be); err != nil {
				v.logger.Warn("failed to remove backend", "vip", key, "backend", addr, "error", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			delete(entry.backends, addr)
		}
	}

	// Add backends not yet present
	for addr, be := range desired {
		if _, ok := entry.backends[addr]; ok {
			entry.backends[addr] = be
			continue
		}

		if err := v.addBackend(ctx, vip, be); err != nil {
			v.logger.Warn("failed to add backend", "vip", key, "backend", addr, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		entry.backends[addr] = be
	}

	if firstErr != nil {
		return fmt.Errorf("failed to reconcile backend set for VIP %s: %w", key, firstErr)
	}

	if len(desired) > 0 {
		applied := 0
		for addr := range desired {
			if _, ok := entry.backends[addr]; ok {
				applied++
			}
		}
		if applied == 0 {
			return fmt.Errorf("failed to apply any healthy backend for VIP %s", key)
		}
	}

	return nil
}

func (v *VPP) addBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	if v.addBackendFn != nil {
		return v.addBackendFn(ctx, vip, backend)
	}
	return v.addBackendLocked(ctx, vip, backend)
}

func (v *VPP) removeBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	if v.removeBackendFn != nil {
		return v.removeBackendFn(ctx, vip, backend)
	}
	return v.removeBackendLocked(ctx, vip, backend)
}

func (v *VPP) addVIP(ctx context.Context, vip *v1alpha1.VirtualIP) error {
	if v.addVIPFn != nil {
		return v.addVIPFn(ctx, vip)
	}
	return v.addVIPLocked(ctx, vip)
}

func (v *VPP) deleteVIP(ctx context.Context, vip *v1alpha1.VirtualIP) error {
	if v.deleteVIPFn != nil {
		return v.deleteVIPFn(ctx, vip)
	}
	return v.deleteVIPLocked(ctx, vip)
}

func (v *VPP) lookupVIP(ctx context.Context, vip *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error) {
	if v.lookupVIPFn != nil {
		return v.lookupVIPFn(ctx, vip)
	}
	return v.lookupVIPLocked(ctx, vip)
}

func (v *VPP) dumpBackends(ctx context.Context, vip *v1alpha1.VirtualIP, desiredBackends []v1alpha1.BackendSpec) (map[string]v1alpha1.BackendSpec, error) {
	if v.dumpBackendsFn != nil {
		return v.dumpBackendsFn(ctx, vip, desiredBackends)
	}
	return v.dumpBackendsLocked(ctx, vip, desiredBackends)
}

func (v *VPP) vipExists(ctx context.Context, vip *v1alpha1.VirtualIP) (bool, error) {
	if v.vipExistsFn != nil {
		return v.vipExistsFn(ctx, vip)
	}
	return v.vipExistsLocked(ctx, vip)
}

func (v *VPP) AddBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	entry, ok := v.vips[key]
	if !ok {
		return fmt.Errorf("VIP %s not found in data plane", key)
	}

	if err := v.addBackend(ctx, vip, backend); err != nil {
		return err
	}

	entry.backends[backend.Address] = backend
	return nil
}

func (v *VPP) RemoveBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	entry, ok := v.vips[key]
	if !ok {
		return nil
	}

	if err := v.removeBackend(ctx, vip, backend); err != nil {
		return err
	}

	delete(entry.backends, backend.Address)
	return nil
}

func (v *VPP) GetState(_ context.Context) (*State, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var vips []VIPState
	for _, entry := range v.vips {
		vs := VIPState{
			Name:     entry.vip.Name,
			Address:  entry.vip.Spec.Address,
			Port:     entry.vip.Spec.Port,
			Protocol: entry.vip.Spec.Protocol,
		}
		for _, be := range entry.backends {
			vs.Backends = append(vs.Backends, BackendEntry{
				Address: be.Address,
				Weight:  be.Weight,
			})
		}
		vips = append(vips, vs)
	}

	return &State{VIPs: vips}, nil
}

func (v *VPP) Close() error {
	v.conn.Disconnect()
	return nil
}

// --- internal VPP API helpers ---

func (v *VPP) newChannel() (api.Channel, error) {
	ch, err := v.conn.NewAPIChannel()
	if err != nil {
		return nil, fmt.Errorf("failed to create API channel: %w", err)
	}
	return ch, nil
}

func (v *VPP) vipExistsLocked(ctx context.Context, vip *v1alpha1.VirtualIP) (bool, error) {
	_, exists, err := v.lookupVIPLocked(ctx, vip)
	return exists, err
}

func (v *VPP) lookupVIPLocked(_ context.Context, vip *v1alpha1.VirtualIP) (*lb.LbVipDetails, bool, error) {
	ch, err := v.newChannel()
	if err != nil {
		return nil, false, err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return nil, false, err
	}
	protocol := protocolNumber(vip.Spec.Protocol)
	port := uint16(vip.Spec.Port)

	reqCtx := ch.SendMultiRequest(&lb.LbVipDump{})
	for {
		detail := &lb.LbVipDetails{}
		stop, err := reqCtx.ReceiveReply(detail)
		if err != nil {
			return nil, false, fmt.Errorf("LbVipDump failed: %w", err)
		}
		if stop {
			break
		}
		if lbVIPMatches(detail.Vip, pfx, protocol, port) {
			return detail, true, nil
		}
	}
	return nil, false, nil
}

func (v *VPP) dumpBackendsLocked(_ context.Context, vip *v1alpha1.VirtualIP, desiredBackends []v1alpha1.BackendSpec) (map[string]v1alpha1.BackendSpec, error) {
	ch, err := v.newChannel()
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return nil, err
	}
	protocol := protocolNumber(vip.Spec.Protocol)
	port := uint16(vip.Spec.Port)

	desiredByAddress := make(map[string]v1alpha1.BackendSpec, len(desiredBackends))
	for _, be := range desiredBackends {
		desiredByAddress[be.Address] = be
	}

	backends := make(map[string]v1alpha1.BackendSpec)
	req := &lb.LbAsDump{
		Pfx:      pfx,
		Protocol: protocol,
		Port:     port,
	}
	reqCtx := ch.SendMultiRequest(req)
	for {
		detail := &lb.LbAsDetails{}
		stop, err := reqCtx.ReceiveReply(detail)
		if err != nil {
			return nil, fmt.Errorf("LbAsDump failed: %w", err)
		}
		if stop {
			break
		}
		if !lbVIPMatches(detail.Vip, pfx, protocol, port) {
			continue
		}

		addr, err := addressToString(detail.AppSrv)
		if err != nil {
			return nil, err
		}
		if be, ok := desiredByAddress[addr]; ok {
			backends[addr] = be
		} else {
			backends[addr] = v1alpha1.BackendSpec{
				Address: addr,
				Weight:  v1alpha1.DefaultBackendWeight,
			}
		}
	}

	return backends, nil
}

func (v *VPP) sameVIPAttributes(existing, desired *v1alpha1.VirtualIP) bool {
	existingAttrs, err := v.effectiveVIPAttributes(existing)
	if err != nil {
		return false
	}
	desiredAttrs, err := v.effectiveVIPAttributes(desired)
	if err != nil {
		return false
	}
	return existingAttrs == desiredAttrs
}

func (v *VPP) effectiveVIPAttributes(vip *v1alpha1.VirtualIP) (vipAttributes, error) {
	encapType := v.config.EncapType
	if vip.Spec.EncapType != "" {
		encapType = string(vip.Spec.EncapType)
	}

	dscp, err := v.effectiveDSCP(encapType, vip)
	if err != nil {
		return vipAttributes{}, err
	}

	return vipAttributes{
		address:             vip.Spec.Address,
		port:                vip.Spec.Port,
		protocol:            vip.Spec.Protocol,
		encapType:           encapType,
		dscp:                dscp,
		serviceType:         v.config.ServiceType,
		newFlowsTableLength: v.config.NewFlowsTableLength,
	}, nil
}

func (v *VPP) effectiveDSCP(encapType string, vip *v1alpha1.VirtualIP) (uint8, error) {
	if encapType != "L3DSR" {
		return 0, nil
	}

	dscp := v.config.DSCP
	if vip.Spec.DSCP != nil {
		dscp = *vip.Spec.DSCP
	}
	if dscp == 0 {
		return 0, fmt.Errorf("invalid dscp=0 for L3DSR; must be 1-63")
	}
	return dscp, nil
}

func (v *VPP) addVIPLocked(_ context.Context, vip *v1alpha1.VirtualIP) error {
	ch, err := v.newChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return err
	}

	encapType := v.config.EncapType
	if vip.Spec.EncapType != "" {
		encapType = string(vip.Spec.EncapType)
	}

	dscp, err := v.effectiveDSCP(encapType, vip)
	if err != nil {
		return err
	}

	req := &lb.LbAddDelVip{
		Pfx:                 pfx,
		Protocol:            protocolNumber(vip.Spec.Protocol),
		Port:                uint16(vip.Spec.Port),
		Encap:               encapToAPI(encapType),
		Dscp:                dscp,
		Type:                serviceTypeToAPI(v.config.ServiceType),
		TargetPort:          uint16(vip.Spec.Port),
		NewFlowsTableLength: v.config.NewFlowsTableLength,
		IsDel:               false,
	}

	reply := &lb.LbAddDelVipReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("LbAddDelVip failed: %w", err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code %d", reply.Retval)
	}
	return nil
}

func (v *VPP) deleteVIPLocked(_ context.Context, vip *v1alpha1.VirtualIP) error {
	ch, err := v.newChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return err
	}

	encapType := v.config.EncapType
	if vip.Spec.EncapType != "" {
		encapType = string(vip.Spec.EncapType)
	}

	dscp, err := v.effectiveDSCP(encapType, vip)
	if err != nil {
		return err
	}

	req := &lb.LbAddDelVip{
		Pfx:      pfx,
		Protocol: protocolNumber(vip.Spec.Protocol),
		Port:     uint16(vip.Spec.Port),
		Encap:    encapToAPI(encapType),
		Dscp:     dscp,
		Type:     serviceTypeToAPI(v.config.ServiceType),
		IsDel:    true,
	}

	reply := &lb.LbAddDelVipReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("LbAddDelVip (delete) failed: %w", err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code %d on delete", reply.Retval)
	}
	return nil
}

func (v *VPP) addBackendLocked(_ context.Context, vip *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
	ch, err := v.newChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return err
	}

	asAddr, err := parseIPAddress(be.Address)
	if err != nil {
		return fmt.Errorf("failed to parse backend address %s: %w", be.Address, err)
	}

	req := &lb.LbAddDelAs{
		Pfx:       pfx,
		Protocol:  protocolNumber(vip.Spec.Protocol),
		Port:      uint16(vip.Spec.Port),
		AsAddress: asAddr,
		IsDel:     false,
		IsFlush:   false,
	}

	reply := &lb.LbAddDelAsReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("LbAddDelAs failed: %w", err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code %d adding backend", reply.Retval)
	}
	return nil
}

func (v *VPP) removeBackendLocked(_ context.Context, vip *v1alpha1.VirtualIP, be v1alpha1.BackendSpec) error {
	ch, err := v.newChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	pfx, err := parseIPPrefix(vip.Spec.Address)
	if err != nil {
		return err
	}

	asAddr, err := parseIPAddress(be.Address)
	if err != nil {
		return fmt.Errorf("failed to parse backend address %s: %w", be.Address, err)
	}

	req := &lb.LbAddDelAs{
		Pfx:       pfx,
		Protocol:  protocolNumber(vip.Spec.Protocol),
		Port:      uint16(vip.Spec.Port),
		AsAddress: asAddr,
		IsDel:     true,
	}

	reply := &lb.LbAddDelAsReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("LbAddDelAs (delete) failed: %w", err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code %d removing backend", reply.Retval)
	}
	return nil
}

// --- helpers ---

func lbVIPMatches(vip lb_types.LbVip, pfx ip_types.AddressWithPrefix, protocol uint8, port uint16) bool {
	return vip.Pfx == pfx && uint8(vip.Protocol) == protocol && vip.Port == port
}

func addressToString(addr ip_types.Address) (string, error) {
	switch addr.Af {
	case ip_types.ADDRESS_IP4:
		ip4 := addr.Un.GetIP4()
		return net.IP(ip4[:]).String(), nil
	case ip_types.ADDRESS_IP6:
		ip6 := addr.Un.GetIP6()
		return net.IP(ip6[:]).String(), nil
	default:
		return "", fmt.Errorf("unsupported VPP address family: %v", addr.Af)
	}
}

func (v *VPP) vipDetailsMatchDesired(vip *v1alpha1.VirtualIP, detail *lb.LbVipDetails) bool {
	attrs, err := v.effectiveVIPAttributes(vip)
	if err != nil {
		return false
	}
	dscpMatches := attrs.encapType != "L3DSR" || uint8(detail.Dscp) == attrs.dscp
	// Flow table length is intentionally excluded here. A retained VIP with
	// matching forwarding attributes can be adopted first and recreated later
	// through a drained rolling repair.
	return detail.Encap == encapToAPI(attrs.encapType) &&
		dscpMatches &&
		detail.SrvType == serviceTypeToAPI(attrs.serviceType) &&
		detail.TargetPort == uint16(attrs.port)
}

func (v *VPP) detectTuningDrifts(vip *v1alpha1.VirtualIP, detail *lb.LbVipDetails) []VIPTuningDrift {
	attrs, err := v.effectiveVIPAttributes(vip)
	if err != nil {
		return nil
	}

	// VPP's add API accepts u32, while the dump API exposes this field as u16.
	// Compare using the dump-width representation to avoid recreating a correctly
	// configured retained VIP forever when the desired value exceeds u16.
	if detail.FlowTableLength == uint16(attrs.newFlowsTableLength) {
		return nil
	}

	return []VIPTuningDrift{{
		Field:   "new_flows_table_length",
		Current: fmt.Sprint(detail.FlowTableLength),
		Desired: fmt.Sprint(attrs.newFlowsTableLength),
	}}
}

func (v *VPP) setTuningDriftsLocked(key string, drifts []VIPTuningDrift) {
	if len(drifts) == 0 {
		v.clearTuningDriftsLocked(key)
		return
	}
	if v.tuningDrifts == nil {
		v.tuningDrifts = make(map[string][]VIPTuningDrift)
	}
	v.tuningDrifts[key] = append([]VIPTuningDrift(nil), drifts...)
}

func (v *VPP) clearTuningDriftsLocked(key string) {
	if v.tuningDrifts == nil {
		return
	}
	delete(v.tuningDrifts, key)
}

func protocolNumber(p v1alpha1.Protocol) uint8 {
	switch p {
	case v1alpha1.ProtocolTCP:
		return 6
	case v1alpha1.ProtocolUDP:
		return 17
	default:
		return 6
	}
}

func encapToAPI(t string) lb_types.LbEncapType {
	switch t {
	case "GRE4":
		return lb_types.LB_API_ENCAP_TYPE_GRE4
	case "GRE6":
		return lb_types.LB_API_ENCAP_TYPE_GRE6
	case "L3DSR":
		return lb_types.LB_API_ENCAP_TYPE_L3DSR
	case "NAT4":
		return lb_types.LB_API_ENCAP_TYPE_NAT4
	case "NAT6":
		return lb_types.LB_API_ENCAP_TYPE_NAT6
	default:
		return lb_types.LB_API_ENCAP_TYPE_GRE4
	}
}

func serviceTypeToAPI(t string) lb_types.LbSrvType {
	switch t {
	case "NODEPORT":
		return lb_types.LB_API_SRV_TYPE_NODEPORT
	default:
		return lb_types.LB_API_SRV_TYPE_CLUSTERIP
	}
}

func parseIPPrefix(addr string) (ip_types.AddressWithPrefix, error) {
	ip := net.ParseIP(addr)
	if ip == nil {
		return ip_types.AddressWithPrefix{}, fmt.Errorf("invalid IP address: %s", addr)
	}

	if ip4 := ip.To4(); ip4 != nil {
		var a [4]byte
		copy(a[:], ip4)
		return ip_types.AddressWithPrefix{
			Address: ip_types.Address{
				Af: ip_types.ADDRESS_IP4,
				Un: ip_types.AddressUnionIP4(ip_types.IP4Address(a)),
			},
			Len: 32,
		}, nil
	}

	var a [16]byte
	copy(a[:], ip.To16())
	return ip_types.AddressWithPrefix{
		Address: ip_types.Address{
			Af: ip_types.ADDRESS_IP6,
			Un: ip_types.AddressUnionIP6(ip_types.IP6Address(a)),
		},
		Len: 128,
	}, nil
}

func parseIPAddress(addr string) (ip_types.Address, error) {
	ip := net.ParseIP(addr)
	if ip == nil {
		return ip_types.Address{}, fmt.Errorf("invalid IP address: %s", addr)
	}

	if ip4 := ip.To4(); ip4 != nil {
		var a [4]byte
		copy(a[:], ip4)
		return ip_types.Address{
			Af: ip_types.ADDRESS_IP4,
			Un: ip_types.AddressUnionIP4(ip_types.IP4Address(a)),
		}, nil
	}

	var a [16]byte
	copy(a[:], ip.To16())
	return ip_types.Address{
		Af: ip_types.ADDRESS_IP6,
		Un: ip_types.AddressUnionIP6(ip_types.IP6Address(a)),
	}, nil
}
