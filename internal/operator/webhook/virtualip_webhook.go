// Package webhook provides admission webhooks for VirtualIP resources.
package webhook

import (
	"context"
	"fmt"
	"net"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

// VirtualIPValidator validates VirtualIP resources on create and update.
type VirtualIPValidator struct{}

var _ admission.Validator[*v1alpha1.VirtualIP] = &VirtualIPValidator{}

// ValidateCreate validates a new VirtualIP.
func (v *VirtualIPValidator) ValidateCreate(_ context.Context, vip *v1alpha1.VirtualIP) (admission.Warnings, error) {
	log.Log.Info("validating VirtualIP create", "name", vip.Name)
	return nil, validateVirtualIP(vip)
}

// ValidateUpdate validates a VirtualIP update.
func (v *VirtualIPValidator) ValidateUpdate(_ context.Context, _ *v1alpha1.VirtualIP, newVIP *v1alpha1.VirtualIP) (admission.Warnings, error) {
	log.Log.Info("validating VirtualIP update", "name", newVIP.Name)
	return nil, validateVirtualIP(newVIP)
}

// ValidateDelete validates a VirtualIP deletion. Always allowed.
func (v *VirtualIPValidator) ValidateDelete(_ context.Context, _ *v1alpha1.VirtualIP) (admission.Warnings, error) {
	return nil, nil
}

// SetupWithManager registers the webhook with the manager.
func (v *VirtualIPValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.VirtualIP{}).
		WithValidator(v).
		Complete()
}

func validateVirtualIP(vip *v1alpha1.VirtualIP) error {
	// Validate VIP address
	if ip := net.ParseIP(vip.Spec.Address); ip == nil {
		return fmt.Errorf("spec.address %q is not a valid IP address", vip.Spec.Address)
	}

	// Validate port range
	if vip.Spec.Port < 1 || vip.Spec.Port > 65535 {
		return fmt.Errorf("spec.port must be between 1 and 65535, got %d", vip.Spec.Port)
	}

	// Validate protocol
	switch vip.Spec.Protocol {
	case v1alpha1.ProtocolTCP, v1alpha1.ProtocolUDP:
		// valid
	default:
		return fmt.Errorf("spec.protocol must be TCP or UDP, got %q", vip.Spec.Protocol)
	}

	// Validate encapType
	switch vip.Spec.EncapType {
	case v1alpha1.EncapTypeGRE4, v1alpha1.EncapTypeGRE6,
		v1alpha1.EncapTypeL3DSR, v1alpha1.EncapTypeNAT4, v1alpha1.EncapTypeNAT6,
		"": // empty is allowed (defaults applied by controller)
	default:
		return fmt.Errorf("spec.encapType %q is not valid", vip.Spec.EncapType)
	}

	// Validate DSCP override for L3DSR when DSCP-based steering is requested.
	if vip.Spec.EncapType == v1alpha1.EncapTypeL3DSR && vip.Spec.DSCP != nil {
		if *vip.Spec.DSCP == 0 || *vip.Spec.DSCP > 63 {
			return fmt.Errorf("spec.dscp must be 1-63 for L3DSR, got %d", *vip.Spec.DSCP)
		}
	}

	// Validate backends
	seenAddr := make(map[string]bool)
	for i, be := range vip.Spec.Backends {
		if ip := net.ParseIP(be.Address); ip == nil {
			return fmt.Errorf("spec.backends[%d].address %q is not a valid IP", i, be.Address)
		}
		if seenAddr[be.Address] {
			return fmt.Errorf("spec.backends[%d].address %q is duplicated", i, be.Address)
		}
		seenAddr[be.Address] = true

		if be.Weight < 1 || be.Weight > 100 {
			return fmt.Errorf("spec.backends[%d].weight must be 1-100, got %d", i, be.Weight)
		}
	}

	// Validate health check
	if hc := vip.Spec.HealthCheck; hc != nil {
		switch hc.Type {
		case v1alpha1.HCTypeHTTP, v1alpha1.HCTypeHTTPS:
			if hc.HTTP == nil {
				return fmt.Errorf("spec.healthCheck.http is required for type %q", hc.Type)
			}
			if hc.HTTP.Port < 1 || hc.HTTP.Port > 65535 {
				return fmt.Errorf("spec.healthCheck.http.port must be 1-65535")
			}
		case v1alpha1.HCTypeTCP:
			if hc.TCP == nil {
				return fmt.Errorf("spec.healthCheck.tcp is required for type tcp")
			}
			if hc.TCP.Port < 1 || hc.TCP.Port > 65535 {
				return fmt.Errorf("spec.healthCheck.tcp.port must be 1-65535")
			}
		case v1alpha1.HCTypePing:
			// no additional config required
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
	}

	return nil
}
