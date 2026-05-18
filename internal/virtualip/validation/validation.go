// Package validation contains VirtualIP semantic validation shared by the
// operator, optional webhook, and agent.
package validation

import (
	"fmt"
	"net"
	"net/http"
	"net/url"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

// Validate validates a VirtualIP object.
func Validate(vip *v1alpha1.VirtualIP) error {
	if err := ValidateDataPlane(vip); err != nil {
		return err
	}
	return ValidateHealthCheckSpec(vip.Spec.HealthCheck)
}

// ValidateDataPlane validates the fields the agent may apply to the data plane
// before health-check-specific validation runs.
func ValidateDataPlane(vip *v1alpha1.VirtualIP) error {
	if vip == nil {
		return fmt.Errorf("VirtualIP is nil")
	}

	return validateCoreSpec(&vip.Spec)
}

// ValidateSpec validates VirtualIP spec fields that are not fully guarded by
// CRD schema validation in all deployment modes.
func ValidateSpec(spec *v1alpha1.VirtualIPSpec) error {
	if err := validateCoreSpec(spec); err != nil {
		return err
	}
	return ValidateHealthCheckSpec(spec.HealthCheck)
}

// ValidateHealthCheckSpec validates health-check-specific VirtualIP fields.
func ValidateHealthCheckSpec(hc *v1alpha1.HealthCheckSpec) error {
	return validateHealthCheckSpec(hc)
}

// ValidateHealthCheckRuntimeSpec validates fields required before a health
// check scheduler is started. It expects CRD-defaulted fields to be materialized.
func ValidateHealthCheckRuntimeSpec(hc *v1alpha1.HealthCheckSpec) error {
	if err := validateHealthCheckSpec(hc); err != nil {
		return err
	}
	if hc == nil {
		return nil
	}
	if hc.RiseCount < 1 {
		return fmt.Errorf("spec.healthCheck.riseCount must be >= 1")
	}
	if hc.FallCount < 1 {
		return fmt.Errorf("spec.healthCheck.fallCount must be >= 1")
	}

	return nil
}

func validateCoreSpec(spec *v1alpha1.VirtualIPSpec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	if ip := net.ParseIP(spec.Address); ip == nil {
		return fmt.Errorf("spec.address %q is not a valid IP address", spec.Address)
	}

	if spec.Port < 1 || spec.Port > 65535 {
		return fmt.Errorf("spec.port must be between 1 and 65535, got %d", spec.Port)
	}

	switch spec.Protocol {
	case v1alpha1.ProtocolTCP, v1alpha1.ProtocolUDP:
	default:
		return fmt.Errorf("spec.protocol must be TCP or UDP, got %q", spec.Protocol)
	}

	return validateDataPlaneSpec(spec)
}

func validateDataPlaneSpec(spec *v1alpha1.VirtualIPSpec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	switch spec.EncapType {
	case v1alpha1.EncapTypeGRE4, v1alpha1.EncapTypeGRE6,
		v1alpha1.EncapTypeL3DSR, v1alpha1.EncapTypeNAT4, v1alpha1.EncapTypeNAT6,
		"": // empty is allowed before defaults are applied
	default:
		return fmt.Errorf("spec.encapType %q is not valid", spec.EncapType)
	}

	if spec.EncapType == v1alpha1.EncapTypeL3DSR && spec.DSCP != nil {
		if *spec.DSCP == 0 || *spec.DSCP > 63 {
			return fmt.Errorf("DSCP must be 1-63 when specified for L3DSR encapsulation")
		}
	}

	seen := make(map[string]bool)
	for i, be := range spec.Backends {
		if ip := net.ParseIP(be.Address); ip == nil {
			return fmt.Errorf("spec.backends[%d].address %q is not a valid IP", i, be.Address)
		}
		if be.MonitorAddress != "" {
			if ip := net.ParseIP(be.MonitorAddress); ip == nil {
				return fmt.Errorf("spec.backends[%d].monitorAddress %q is not a valid IP", i, be.MonitorAddress)
			}
		}
		if seen[be.Address] {
			return fmt.Errorf("spec.backends[%d].address %q is duplicated", i, be.Address)
		}
		seen[be.Address] = true

		if be.Weight < 1 || be.Weight > 100 {
			return fmt.Errorf("spec.backends[%d].weight must be 1-100, got %d", i, be.Weight)
		}
	}

	return nil
}

func validateHealthCheckSpec(hc *v1alpha1.HealthCheckSpec) error {
	if hc == nil {
		return nil
	}

	switch hc.Type {
	case v1alpha1.HCTypeHTTP, v1alpha1.HCTypeHTTPS:
		if hc.HTTP == nil {
			return fmt.Errorf("spec.healthCheck.http is required for type %q", hc.Type)
		}
		if hc.HTTP.Port < 1 || hc.HTTP.Port > 65535 {
			return fmt.Errorf("spec.healthCheck.http.port must be 1-65535")
		}
		switch hc.HTTP.Method {
		case "", http.MethodGet, http.MethodHead, http.MethodPost:
		default:
			return fmt.Errorf("spec.healthCheck.http.method must be one of GET, HEAD, POST, got %q", hc.HTTP.Method)
		}
		if hc.HTTP.Path != "" {
			parsedPath, err := url.Parse(hc.HTTP.Path)
			if err != nil {
				return fmt.Errorf("spec.healthCheck.http.path %q is not valid: %w", hc.HTTP.Path, err)
			}
			if parsedPath.IsAbs() || parsedPath.Host != "" {
				return fmt.Errorf("spec.healthCheck.http.path must be relative, got %q", hc.HTTP.Path)
			}
		}
		for i, code := range hc.HTTP.ExpectedCodes {
			if code < 100 || code > 599 {
				return fmt.Errorf("spec.healthCheck.http.expectedCodes[%d] must be 100-599, got %d", i, code)
			}
		}
	case v1alpha1.HCTypeTCP, v1alpha1.HCTypeTLSHello:
		if hc.TCP == nil {
			return fmt.Errorf("spec.healthCheck.tcp is required for type %q", hc.Type)
		}
		if hc.TCP.Port < 1 || hc.TCP.Port > 65535 {
			return fmt.Errorf("spec.healthCheck.tcp.port must be 1-65535")
		}
	case v1alpha1.HCTypePing:
	default:
		return fmt.Errorf("spec.healthCheck.type %q is not valid", hc.Type)
	}

	if hc.IntervalSeconds < 1 {
		return fmt.Errorf("spec.healthCheck.intervalSeconds must be >= 1")
	}
	if hc.TimeoutSeconds < 1 {
		return fmt.Errorf("spec.healthCheck.timeoutSeconds must be >= 1")
	}
	if hc.TimeoutSeconds >= hc.IntervalSeconds {
		return fmt.Errorf("spec.healthCheck.timeoutSeconds must be less than intervalSeconds")
	}

	return nil
}
