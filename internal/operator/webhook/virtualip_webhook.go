// Package webhook provides admission webhooks for VirtualIP resources.
package webhook

import (
	"context"

	vipvalidation "github.com/akam1o/arca-lb/internal/virtualip/validation"
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
	return vipvalidation.Validate(vip)
}
