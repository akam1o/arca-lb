package datastore

import (
	"fmt"
	"net"
	"strings"
	"unicode"

	"github.com/akam1o/arca-lb/internal/common/models"
)

// MaxResourceIDBytes is the maximum accepted byte length for datastore resource IDs.
const MaxResourceIDBytes = 256

// ValidateResourceID validates IDs before they are used in datastore keys or queries.
func ValidateResourceID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	if len(value) > MaxResourceIDBytes {
		return fmt.Errorf("%w: %s must be at most %d bytes", ErrInvalidInput, field, MaxResourceIDBytes)
	}
	if strings.Contains(value, "/") {
		return fmt.Errorf("%w: %s must not contain '/'", ErrInvalidInput, field)
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: %s must not contain whitespace", ErrInvalidInput, field)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s must not contain control characters", ErrInvalidInput, field)
	}
	return nil
}

func validateOptionalResourceID(field, value string) error {
	if value == "" {
		return nil
	}
	return ValidateResourceID(field, value)
}

// ValidateVIPForWrite validates VIP fields before a datastore mutation.
func ValidateVIPForWrite(vip *models.VIP) error {
	if vip == nil {
		return ErrInvalidInput
	}
	if err := validateOptionalResourceID("vip id", vip.ID); err != nil {
		return err
	}
	vipIP := net.ParseIP(vip.VIP)
	if vipIP == nil {
		return fmt.Errorf("%w: vip must be a valid IP address", ErrInvalidInput)
	}
	if vip.Port < 1 || vip.Port > 65535 {
		return fmt.Errorf("%w: vip port must be between 1 and 65535", ErrInvalidInput)
	}
	switch vip.Protocol {
	case models.ProtocolTCP, models.ProtocolUDP:
	default:
		return fmt.Errorf("%w: vip protocol must be TCP or UDP", ErrInvalidInput)
	}
	if vip.LBMethod != "" && vip.LBMethod != models.LBMethodMaglev {
		return fmt.Errorf("%w: vip lb_method is unsupported", ErrInvalidInput)
	}
	switch vip.EncapType {
	case "", models.EncapTypeGRE4, models.EncapTypeGRE6, models.EncapTypeL3DSR, models.EncapTypeNAT4, models.EncapTypeNAT6:
	default:
		return fmt.Errorf("%w: vip encap_type is unsupported", ErrInvalidInput)
	}
	encapType := models.EffectiveEncapType(vip.EncapType)
	if err := validateVIPAddressFamily(vipIP, encapType); err != nil {
		return err
	}
	if vip.DSCP != nil {
		if *vip.DSCP > 63 {
			return fmt.Errorf("%w: vip dscp must be between 0 and 63", ErrInvalidInput)
		}
		if encapType == models.EncapTypeL3DSR && *vip.DSCP == 0 {
			return fmt.Errorf("%w: vip dscp must be between 1 and 63 for L3DSR", ErrInvalidInput)
		}
	}
	if vip.HealthCheck != nil {
		if err := models.ValidateHealthCheckTiming(vip.HealthCheck); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if err := models.ValidateHealthCheckConfig(vip.HealthCheck.Type, vip.HealthCheck.Config); err != nil {
			return fmt.Errorf("%w: invalid health check config: %v", ErrInvalidInput, err)
		}
	}
	return nil
}

// ValidateBackendFieldsForWrite validates Backend fields that are mutable on writes.
func ValidateBackendFieldsForWrite(backend *models.Backend) error {
	if backend == nil {
		return ErrInvalidInput
	}
	if err := validateOptionalResourceID("backend id", backend.ID); err != nil {
		return err
	}
	if net.ParseIP(backend.IP) == nil {
		return fmt.Errorf("%w: backend ip must be a valid IP address", ErrInvalidInput)
	}
	if backend.Weight < 1 || backend.Weight > 100 {
		return fmt.Errorf("%w: backend weight must be between 1 and 100", ErrInvalidInput)
	}
	return nil
}

func validateVIPAddressFamily(ip net.IP, encapType models.EncapType) error {
	isIPv4 := ip.To4() != nil
	switch {
	case models.EncapRequiresIPv4VIP(encapType):
		if !isIPv4 {
			return fmt.Errorf("%w: vip must be IPv4 when encap_type is %s", ErrInvalidInput, encapType)
		}
	case models.EncapRequiresIPv6VIP(encapType):
		if isIPv4 {
			return fmt.Errorf("%w: vip must be IPv6 when encap_type is %s", ErrInvalidInput, encapType)
		}
	}
	return nil
}

// ValidateBackendAddressFamilyForVIP validates backend address-family invariants
// against the effective encapsulation mode of its parent VIP.
func ValidateBackendAddressFamilyForVIP(vip *models.VIP, backend *models.Backend) error {
	if backend == nil {
		return ErrInvalidInput
	}
	return ValidateBackendIPFamilyForVIP(vip, backend.IP)
}

// ValidateBackendIPFamilyForVIP validates a backend IP against its parent VIP.
func ValidateBackendIPFamilyForVIP(vip *models.VIP, backendIP string) error {
	if vip == nil {
		return ErrInvalidInput
	}
	ip := net.ParseIP(backendIP)
	if ip == nil {
		return fmt.Errorf("%w: backend ip must be a valid IP address", ErrInvalidInput)
	}

	encapType := models.EffectiveEncapType(vip.EncapType)
	isIPv4 := ip.To4() != nil
	switch {
	case models.EncapRequiresIPv4Backend(encapType):
		if !isIPv4 {
			return fmt.Errorf("%w: backend ip must be IPv4 when encap_type is %s", ErrInvalidInput, encapType)
		}
	case models.EncapRequiresIPv6Backend(encapType):
		if isIPv4 {
			return fmt.Errorf("%w: backend ip must be IPv6 when encap_type is %s", ErrInvalidInput, encapType)
		}
	}
	return nil
}

// ValidateBackendFamiliesForVIP validates all existing backends for a VIP.
func ValidateBackendFamiliesForVIP(vip *models.VIP, backends []models.Backend) error {
	for i := range backends {
		if err := ValidateBackendAddressFamilyForVIP(vip, &backends[i]); err != nil {
			return err
		}
	}
	return nil
}

// ValidateBackendForWrite validates Backend fields before a datastore mutation.
func ValidateBackendForWrite(backend *models.Backend) error {
	if err := ValidateBackendFieldsForWrite(backend); err != nil {
		return err
	}
	if err := ValidateResourceID("backend vip_id", backend.VIPID); err != nil {
		return err
	}
	return nil
}
