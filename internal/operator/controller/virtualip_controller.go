// Package controller implements the Kubernetes controller for VirtualIP resources.
package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

const (
	finalizerName = "arca.io/virtualip-protection"
)

// VirtualIPReconciler reconciles a VirtualIP object.
type VirtualIPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=arca.io,resources=virtualips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arca.io,resources=virtualips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arca.io,resources=virtualips/finalizers,verbs=update

// Reconcile handles VirtualIP create/update/delete events.
// The operator's role is to:
// 1. Ensure the finalizer is present for cleanup
// 2. Set default values and validate
// 3. Update the status with backend count and conditions
//
// The actual data-plane programming is done by the Agent (watching the same CRDs).
func (r *VirtualIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the VirtualIP
	var vip v1alpha1.VirtualIP
	if err := r.Get(ctx, req.NamespacedName, &vip); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("VirtualIP deleted, nothing to reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get VirtualIP: %w", err)
	}

	// Handle deletion
	if !vip.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&vip, finalizerName) {
			// Perform cleanup logic if needed (agents handle their own cleanup via watch)
			logger.Info("VirtualIP being deleted, removing finalizer")

			controllerutil.RemoveFinalizer(&vip, finalizerName)
			if err := r.Update(ctx, &vip); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&vip, finalizerName) {
		controllerutil.AddFinalizer(&vip, finalizerName)
		if err := r.Update(ctx, &vip); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		// Requeue to continue reconciliation with the updated object
		return ctrl.Result{Requeue: true}, nil
	}

	// Apply defaults
	changed := applyDefaults(&vip)
	if changed {
		if err := r.Update(ctx, &vip); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply defaults: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update status
	if err := r.updateStatus(ctx, &vip); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("VirtualIP reconciled",
		"address", vip.Spec.Address,
		"port", vip.Spec.Port,
		"backends", len(vip.Spec.Backends))

	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *VirtualIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualIP{}).
		Complete(r)
}

func (r *VirtualIPReconciler) updateStatus(ctx context.Context, vip *v1alpha1.VirtualIP) error {
	newStatus := v1alpha1.VirtualIPStatus{
		// Backend health is observed by the agent. Do not advance this generation
		// from the operator while carrying forward agent-owned health fields.
		ObservedGeneration: vip.Status.ObservedGeneration,
		TotalBackends:      len(vip.Spec.Backends),
		HealthyBackends:    vip.Status.HealthyBackends, // Preserved from agent updates
		Backends:           vip.Status.Backends,        // Preserved from agent updates
	}

	// Set Ready condition based on configuration validity
	readyCond := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: vip.Generation,
	}

	if err := validateSpec(&vip.Spec); err != nil {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = "InvalidSpec"
		readyCond.Message = err.Error()
	} else if newStatus.TotalBackends == 0 {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = "NoBackends"
		readyCond.Message = "No backends configured"
	} else {
		readyCond.Status = metav1.ConditionTrue
		readyCond.Reason = "Configured"
		readyCond.Message = fmt.Sprintf("%d backends configured", newStatus.TotalBackends)
	}

	newStatus.Conditions = vip.Status.Conditions
	meta.SetStatusCondition(&newStatus.Conditions, readyCond)

	if !equality.Semantic.DeepEqual(vip.Status, newStatus) {
		vip.Status = newStatus
		if err := r.Status().Update(ctx, vip); err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}
	}

	return nil
}

func applyDefaults(vip *v1alpha1.VirtualIP) bool {
	changed := false

	if vip.Spec.EncapType == "" {
		vip.Spec.EncapType = v1alpha1.EncapTypeL3DSR
		changed = true
	}

	for i := range vip.Spec.Backends {
		if vip.Spec.Backends[i].Weight == 0 {
			vip.Spec.Backends[i].Weight = v1alpha1.DefaultBackendWeight
			changed = true
		}
	}

	if hc := vip.Spec.HealthCheck; hc != nil {
		if hc.IntervalSeconds == 0 {
			hc.IntervalSeconds = 5
			changed = true
		}
		if hc.TimeoutSeconds == 0 {
			hc.TimeoutSeconds = 3
			changed = true
		}
		if hc.RiseCount == 0 {
			hc.RiseCount = 3
			changed = true
		}
		if hc.FallCount == 0 {
			hc.FallCount = 2
			changed = true
		}
	}

	return changed
}

func validateSpec(spec *v1alpha1.VirtualIPSpec) error {
	if spec.EncapType == v1alpha1.EncapTypeL3DSR && spec.DSCP != nil {
		if *spec.DSCP == 0 || *spec.DSCP > 63 {
			return fmt.Errorf("DSCP must be 1-63 when specified for L3DSR encapsulation")
		}
	}

	// Check for duplicate backend addresses
	seen := make(map[string]bool)
	for _, be := range spec.Backends {
		if seen[be.Address] {
			return fmt.Errorf("duplicate backend address: %s", be.Address)
		}
		seen[be.Address] = true
	}

	if err := validateHealthCheckSpec(spec.HealthCheck); err != nil {
		return err
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
	case v1alpha1.HCTypeTCP, v1alpha1.HCTypeTLSHello:
		if hc.TCP == nil {
			return fmt.Errorf("spec.healthCheck.tcp is required for type %q", hc.Type)
		}
		if hc.TCP.Port < 1 || hc.TCP.Port > 65535 {
			return fmt.Errorf("spec.healthCheck.tcp.port must be 1-65535")
		}
	case v1alpha1.HCTypePing:
		// No additional config required.
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
