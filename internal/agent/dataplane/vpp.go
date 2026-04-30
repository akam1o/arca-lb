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
	SocketPath            string
	ConnectTimeout        time.Duration
	ReconnectInterval     time.Duration
	EncapType             string
	DSCP                  uint8
	ServiceType           string
	NewFlowsTableLength   uint32
	FailOnAllBackendsDown bool
}

func vppConfigFromMap(m map[string]interface{}) VPPConfig {
	cfg := VPPConfig{
		SocketPath:          "/run/vpp/api.sock",
		ConnectTimeout:      10 * time.Second,
		ReconnectInterval:   5 * time.Second,
		EncapType:           "L3DSR",
		DSCP:                10,
		ServiceType:         "CLUSTERIP",
		NewFlowsTableLength: 65537,
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
	return cfg
}

// VPP implements DataPlane using fd.io VPP.
type VPP struct {
	config VPPConfig
	conn   *core.Connection
	logger *slog.Logger

	mu   sync.RWMutex
	vips map[string]*vipEntry // key: namespace/name
}

type vipEntry struct {
	vip      *v1alpha1.VirtualIP
	backends map[string]v1alpha1.BackendSpec // key: address
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
		config: config,
		conn:   conn,
		logger: slog.Default().With("component", "dataplane-vpp"),
		vips:   make(map[string]*vipEntry),
	}, nil
}

func (v *VPP) vipKey(vip *v1alpha1.VirtualIP) string {
	return vip.Namespace + "/" + vip.Name
}

func (v *VPP) ApplyVIP(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)

	existing, exists := v.vips[key]
	if exists && !v.sameVIPAttributes(existing.vip, vip) {
		if err := v.deleteVIPLocked(ctx, existing.vip); err != nil {
			return fmt.Errorf("failed to delete existing VIP for update: %w", err)
		}
		delete(v.vips, key)
		exists = false
	}

	if !exists {
		if err := v.addVIPLocked(ctx, vip); err != nil {
			return fmt.Errorf("failed to add VIP: %w", err)
		}

		existing = &vipEntry{
			vip:      vip.DeepCopy(),
			backends: make(map[string]v1alpha1.BackendSpec),
		}
		v.vips[key] = existing
	}

	v.reconcileBackendsLocked(ctx, key, existing, vip, healthyBackends)
	existing.vip = vip.DeepCopy()

	return nil
}

func (v *VPP) RemoveVIP(ctx context.Context, vip *v1alpha1.VirtualIP) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	if _, ok := v.vips[key]; !ok {
		return nil // already removed
	}

	if err := v.deleteVIPLocked(ctx, vip); err != nil {
		return err
	}

	delete(v.vips, key)
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

	v.reconcileBackendsLocked(ctx, key, entry, vip, backends)
	return nil
}

func (v *VPP) reconcileBackendsLocked(
	ctx context.Context,
	key string,
	entry *vipEntry,
	vip *v1alpha1.VirtualIP,
	backends []v1alpha1.BackendSpec,
) {
	desired := make(map[string]v1alpha1.BackendSpec)
	for _, be := range backends {
		desired[be.Address] = be
	}

	// Remove backends not in desired set
	for addr, be := range entry.backends {
		if _, ok := desired[addr]; !ok {
			if err := v.removeBackendLocked(ctx, vip, be); err != nil {
				v.logger.Warn("failed to remove backend", "vip", key, "backend", addr, "error", err)
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

		if err := v.addBackendLocked(ctx, vip, be); err != nil {
			v.logger.Warn("failed to add backend", "vip", key, "backend", addr, "error", err)
			continue
		}
		entry.backends[addr] = be
	}
}

func (v *VPP) AddBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	key := v.vipKey(vip)
	entry, ok := v.vips[key]
	if !ok {
		return fmt.Errorf("VIP %s not found in data plane", key)
	}

	if err := v.addBackendLocked(ctx, vip, backend); err != nil {
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

	if err := v.removeBackendLocked(ctx, vip, backend); err != nil {
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

	dscp := v.config.DSCP
	if encapType == "L3DSR" {
		if vip.Spec.DSCP != nil {
			dscp = *vip.Spec.DSCP
		}
		if dscp == 0 {
			return vipAttributes{}, fmt.Errorf("invalid dscp=0 for L3DSR; must be 1-63")
		}
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

	dscp := v.config.DSCP
	if encapType == "L3DSR" {
		if vip.Spec.DSCP != nil {
			dscp = *vip.Spec.DSCP
		}
		if dscp == 0 {
			return fmt.Errorf("invalid dscp=0 for L3DSR; must be 1-63")
		}
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

	dscp := v.config.DSCP
	if encapType == "L3DSR" && vip.Spec.DSCP != nil {
		dscp = *vip.Spec.DSCP
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
