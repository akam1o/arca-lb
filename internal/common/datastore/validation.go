package datastore

import (
	"fmt"
	"net"

	"github.com/akam1o/arca-lb/internal/common/models"
)

// ValidateVIPForWrite validates VIP fields before a datastore mutation.
func ValidateVIPForWrite(vip *models.VIP) error {
	if vip == nil {
		return ErrInvalidInput
	}
	if net.ParseIP(vip.VIP) == nil {
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
	if vip.DSCP != nil && *vip.DSCP > 63 {
		return fmt.Errorf("%w: vip dscp must be between 0 and 63", ErrInvalidInput)
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
	if net.ParseIP(backend.IP) == nil {
		return fmt.Errorf("%w: backend ip must be a valid IP address", ErrInvalidInput)
	}
	if backend.Weight < 1 || backend.Weight > 100 {
		return fmt.Errorf("%w: backend weight must be between 1 and 100", ErrInvalidInput)
	}
	return nil
}

// ValidateBackendForWrite validates Backend fields before a datastore mutation.
func ValidateBackendForWrite(backend *models.Backend) error {
	if err := ValidateBackendFieldsForWrite(backend); err != nil {
		return err
	}
	if backend.VIPID == "" {
		return ErrInvalidInput
	}
	return nil
}
