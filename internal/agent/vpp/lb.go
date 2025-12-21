package vpp

import (
	"fmt"
	"net"

	"git.fd.io/govpp.git/binapi/ip_types"
	"git.fd.io/govpp.git/binapi/lb"
	"git.fd.io/govpp.git/binapi/lb_types"
	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// LBManager manages VPP load balancer operations
type LBManager struct {
	conn   *Connection
	logger *logrus.Logger
	config *config.VPPLBConfig
}

// NewLBManager creates a new LBManager
func NewLBManager(conn *Connection, lbConfig *config.VPPLBConfig, logger *logrus.Logger) *LBManager {
	return &LBManager{
		conn:   conn,
		logger: logger,
		config: lbConfig,
	}
}

// AddVIP adds a VIP to VPP
func (m *LBManager) AddVIP(vipConfig *models.VIPConfig) error {
	m.logger.WithFields(logrus.Fields{
		"vip_id":   vipConfig.VIP.ID,
		"vip":      vipConfig.VIP.VIP,
		"port":     vipConfig.VIP.Port,
		"protocol": vipConfig.VIP.Protocol,
	}).Info("Adding VIP to VPP")

	// Create API channel
	ch, err := m.conn.NewStream()
	if err != nil {
		return fmt.Errorf("failed to create API channel: %w", err)
	}
	defer ch.Close()

	// Parse VIP address
	vipAddr, err := parseIPAddress(vipConfig.VIP.VIP)
	if err != nil {
		return fmt.Errorf("failed to parse VIP address: %w", err)
	}

	// Convert protocol
	var proto uint8
	switch vipConfig.VIP.Protocol {
	case models.ProtocolTCP:
		proto = 6 // TCP
	case models.ProtocolUDP:
		proto = 17 // UDP
	default:
		return fmt.Errorf("unsupported protocol: %s", vipConfig.VIP.Protocol)
	}

	// Prepare LB add/del VIP request using configured parameters
	req := &lb.LbAddDelVip{
		Pfx:                 vipAddr,
		Protocol:            proto,
		Port:                uint16(vipConfig.VIP.Port),
		Encap:               encapTypeToAPI(m.config.EncapType),
		Dscp:                m.config.DSCP,
		Type:                lbTypeToAPI(m.config.Type),
		TargetPort:          uint16(vipConfig.VIP.Port),
		NodePort:            0,
		NewFlowsTableLength: m.config.NewFlowsTableLength,
		IsDel:               false,
	}

	reply := &lb.LbAddDelVipReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("failed to add VIP: %w", err)
	}

	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code: %d", reply.Retval)
	}

	m.logger.WithField("vip_id", vipConfig.VIP.ID).Info("Successfully added VIP to VPP")

	// Add all backends using AddBackendToVIP which properly handles the VIP context
	var addErrors []error
	for i := range vipConfig.Backends {
		if err := m.AddBackendToVIP(vipConfig, &vipConfig.Backends[i]); err != nil {
			m.logger.WithError(err).WithField("backend_id", vipConfig.Backends[i].ID).
				Warn("Failed to add backend during VIP creation")
			addErrors = append(addErrors, err)
			// Continue adding other backends - partial failure is acceptable
		}
	}

	// Check if all backends failed to add
	if len(addErrors) > 0 && len(addErrors) == len(vipConfig.Backends) {
		if m.config.FailOnAllBackendsDown {
			// Configuration requires VIP creation to fail if no backends can be added
			m.logger.WithField("vip_id", vipConfig.VIP.ID).
				Error("VIP creation failed: all backends failed to add")
			// Clean up the VIP we just created
			if delErr := m.DeleteVIPByConfig(vipConfig); delErr != nil {
				m.logger.WithError(delErr).Error("Failed to clean up VIP after backend failures")
			}
			return fmt.Errorf("all %d backends failed to add", len(addErrors))
		}
		// Configuration allows VIP creation without backends (can be added later)
		m.logger.WithField("vip_id", vipConfig.VIP.ID).
			Warn("VIP created but all backends failed to add (backends can be added later)")
	}

	return nil
}

// DeleteVIP deletes a VIP from VPP
// Note: This method requires the VIP configuration to be available
// Use sync.DeleteVIP which maintains the configuration cache
func (m *LBManager) DeleteVIP(vipID string) error {
	return fmt.Errorf("DeleteVIP requires full VIP config - use sync.DeleteVIP instead")
}

// DeleteVIPByConfig deletes a VIP from VPP using full configuration
func (m *LBManager) DeleteVIPByConfig(vipConfig *models.VIPConfig) error {
	m.logger.WithFields(logrus.Fields{
		"vip_id": vipConfig.VIP.ID,
		"vip":    vipConfig.VIP.VIP,
		"port":   vipConfig.VIP.Port,
	}).Info("Deleting VIP from VPP")

	// Create API channel
	ch, err := m.conn.NewStream()
	if err != nil {
		return fmt.Errorf("failed to create API channel: %w", err)
	}
	defer ch.Close()

	// Parse VIP address
	vipAddr, err := parseIPAddress(vipConfig.VIP.VIP)
	if err != nil {
		return fmt.Errorf("failed to parse VIP address: %w", err)
	}

	// Convert protocol
	var proto uint8
	switch vipConfig.VIP.Protocol {
	case models.ProtocolTCP:
		proto = 6 // TCP
	case models.ProtocolUDP:
		proto = 17 // UDP
	default:
		return fmt.Errorf("unsupported protocol: %s", vipConfig.VIP.Protocol)
	}

	// Prepare LB add/del VIP request (delete) - must match creation parameters
	req := &lb.LbAddDelVip{
		Pfx:      vipAddr,
		Protocol: proto,
		Port:     uint16(vipConfig.VIP.Port),
		Encap:    encapTypeToAPI(m.config.EncapType),
		Dscp:     m.config.DSCP,
		Type:     lbTypeToAPI(m.config.Type),
		IsDel:    true,
	}

	reply := &lb.LbAddDelVipReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("failed to delete VIP: %w", err)
	}

	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code: %d", reply.Retval)
	}

	m.logger.WithField("vip_id", vipConfig.VIP.ID).Info("Successfully deleted VIP from VPP")

	return nil
}

// AddBackend adds a backend server to a VIP
// Note: This method requires the VIP configuration to be available
// Use AddBackendToVIP with full VIPConfig instead
func (m *LBManager) AddBackend(vipID string, backend *models.Backend) error {
	return fmt.Errorf("AddBackend requires full VIP config - use AddBackendToVIP instead")
}

// AddBackendToVIP adds a backend server to a specific VIP configuration
func (m *LBManager) AddBackendToVIP(vipConfig *models.VIPConfig, backend *models.Backend) error {
	m.logger.WithFields(logrus.Fields{
		"vip_id":     vipConfig.VIP.ID,
		"backend_id": backend.ID,
		"backend_ip": backend.IP,
	}).Info("Adding backend to VIP in VPP")

	// Create API channel
	ch, err := m.conn.NewStream()
	if err != nil {
		return fmt.Errorf("failed to create API channel: %w", err)
	}
	defer ch.Close()

	// Parse VIP address
	vipAddr, err := parseIPAddress(vipConfig.VIP.VIP)
	if err != nil {
		return fmt.Errorf("failed to parse VIP address: %w", err)
	}

	// Parse backend IP
	backendAddr, err := parseIPAddress(backend.IP)
	if err != nil {
		return fmt.Errorf("failed to parse backend IP: %w", err)
	}

	// Convert protocol
	var proto uint8
	switch vipConfig.VIP.Protocol {
	case models.ProtocolTCP:
		proto = 6 // TCP
	case models.ProtocolUDP:
		proto = 17 // UDP
	default:
		return fmt.Errorf("unsupported protocol: %s", vipConfig.VIP.Protocol)
	}

	// Prepare LB add/del AS (Application Server) request
	req := &lb.LbAddDelAs{
		Pfx:       vipAddr,
		Protocol:  proto,
		Port:      uint16(vipConfig.VIP.Port),
		AsAddress: backendAddr.Address,
		IsDel:     false,
		IsFlush:   false,
	}

	reply := &lb.LbAddDelAsReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("failed to add backend: %w", err)
	}

	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code: %d", reply.Retval)
	}

	m.logger.WithFields(logrus.Fields{
		"vip_id":     vipConfig.VIP.ID,
		"backend_id": backend.ID,
	}).Info("Successfully added backend to VIP in VPP")

	return nil
}

// DeleteBackend deletes a backend server from a VIP
// Note: This method requires the VIP and backend configuration to be available
// Use DeleteBackendFromVIP with full configs instead
func (m *LBManager) DeleteBackend(vipID string, backendID string) error {
	return fmt.Errorf("DeleteBackend requires full VIP and backend config - use DeleteBackendFromVIP instead")
}

// DeleteBackendFromVIP deletes a backend server from a specific VIP configuration
func (m *LBManager) DeleteBackendFromVIP(vipConfig *models.VIPConfig, backend *models.Backend) error {
	m.logger.WithFields(logrus.Fields{
		"vip_id":     vipConfig.VIP.ID,
		"backend_id": backend.ID,
		"backend_ip": backend.IP,
	}).Info("Deleting backend from VIP in VPP")

	// Create API channel
	ch, err := m.conn.NewStream()
	if err != nil {
		return fmt.Errorf("failed to create API channel: %w", err)
	}
	defer ch.Close()

	// Parse VIP address
	vipAddr, err := parseIPAddress(vipConfig.VIP.VIP)
	if err != nil {
		return fmt.Errorf("failed to parse VIP address: %w", err)
	}

	// Parse backend IP
	backendAddr, err := parseIPAddress(backend.IP)
	if err != nil {
		return fmt.Errorf("failed to parse backend IP: %w", err)
	}

	// Convert protocol
	var proto uint8
	switch vipConfig.VIP.Protocol {
	case models.ProtocolTCP:
		proto = 6 // TCP
	case models.ProtocolUDP:
		proto = 17 // UDP
	default:
		return fmt.Errorf("unsupported protocol: %s", vipConfig.VIP.Protocol)
	}

	// Prepare LB add/del AS (Application Server) request (delete)
	req := &lb.LbAddDelAs{
		Pfx:       vipAddr,
		Protocol:  proto,
		Port:      uint16(vipConfig.VIP.Port),
		AsAddress: backendAddr.Address,
		IsDel:     true,
		IsFlush:   false,
	}

	reply := &lb.LbAddDelAsReply{}
	if err := ch.SendRequest(req).ReceiveReply(reply); err != nil {
		return fmt.Errorf("failed to delete backend: %w", err)
	}

	if reply.Retval != 0 {
		return fmt.Errorf("VPP returned error code: %d", reply.Retval)
	}

	m.logger.WithFields(logrus.Fields{
		"vip_id":     vipConfig.VIP.ID,
		"backend_id": backend.ID,
	}).Info("Successfully deleted backend from VIP in VPP")

	return nil
}

// parseIPAddress converts a string IP address to VPP AddressWithPrefix format
func parseIPAddress(ipStr string) (ip_types.AddressWithPrefix, error) {
	// Try parsing as CIDR first
	ip, ipNet, err := net.ParseCIDR(ipStr)
	if err != nil {
		// Try parsing as plain IP
		ip = net.ParseIP(ipStr)
		if ip == nil {
			return ip_types.AddressWithPrefix{}, fmt.Errorf("invalid IP address: %s", ipStr)
		}
		// Default to /32 for IPv4 or /128 for IPv6
		if ip.To4() != nil {
			ipNet = &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
		} else {
			ipNet = &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
		}
	}

	// Convert to VPP format
	var addr ip_types.Address
	if ip.To4() != nil {
		// IPv4
		addr.Af = ip_types.ADDRESS_IP4
		copy(addr.Un.XXX_UnionData[:], ip.To4())
	} else {
		// IPv6
		addr.Af = ip_types.ADDRESS_IP6
		copy(addr.Un.XXX_UnionData[:], ip.To16())
	}

	prefixLen, _ := ipNet.Mask.Size()

	return ip_types.AddressWithPrefix{
		Address: addr,
		Len:     uint8(prefixLen),
	}, nil
}

// encapTypeToAPI converts string encap type to VPP API enum
func encapTypeToAPI(encapType string) lb_types.LbEncapType {
	switch encapType {
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
		return lb_types.LB_API_ENCAP_TYPE_GRE4 // Default to GRE4
	}
}

// lbTypeToAPI converts string LB type to VPP API enum
func lbTypeToAPI(lbType string) lb_types.LbSrvType {
	switch lbType {
	case "CLUSTERIP":
		return lb_types.LB_API_SRV_TYPE_CLUSTERIP
	case "NODEPORT":
		return lb_types.LB_API_SRV_TYPE_NODEPORT
	default:
		return lb_types.LB_API_SRV_TYPE_CLUSTERIP // Default to CLUSTERIP
	}
}
