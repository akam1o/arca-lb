// Package status updates Kubernetes VirtualIP status from agent observations.
package status

import (
	"context"
	"fmt"
	"log/slog"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Config configures Kubernetes API access for status updates.
type Config struct {
	// Kubeconfig path. Empty uses in-cluster config.
	Kubeconfig string
}

// Updater writes agent-observed backend health to VirtualIP status.
type Updater struct {
	client client.Client
	logger *slog.Logger
}

// NewUpdater creates a Kubernetes-backed status updater.
func NewUpdater(cfg Config, logger *slog.Logger) (*Updater, error) {
	restCfg, err := buildRESTConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build REST config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add VirtualIP scheme: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create status client: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Updater{
		client: k8sClient,
		logger: logger.With("component", "status-updater"),
	}, nil
}

// UpdateVIPStatus updates backend health fields in the VirtualIP status subresource.
func (u *Updater) UpdateVIPStatus(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error {
	if u == nil || u.client == nil || vip == nil {
		return nil
	}

	key := types.NamespacedName{Namespace: vip.Namespace, Name: vip.Name}
	healthySet := make(map[string]struct{}, len(healthyBackends))
	for _, be := range healthyBackends {
		healthySet[be.Address] = struct{}{}
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current v1alpha1.VirtualIP
		if err := u.client.Get(ctx, key, &current); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if current.UID != vip.UID || current.Generation != vip.Generation || !current.DeletionTimestamp.IsZero() {
			u.logger.Debug("skipping stale VirtualIP status update",
				"namespace", vip.Namespace,
				"name", vip.Name,
				"observed_generation", vip.Generation,
				"current_generation", current.Generation)
			return nil
		}

		current.Status.ObservedGeneration = vip.Generation
		current.Status.TotalBackends = len(vip.Spec.Backends)
		current.Status.HealthyBackends = len(healthyBackends)
		current.Status.Backends = buildBackendStatuses(vip.Spec.Backends, healthySet)

		return u.client.Status().Update(ctx, &current)
	})
}

func buildBackendStatuses(backends []v1alpha1.BackendSpec, healthySet map[string]struct{}) []v1alpha1.BackendStatus {
	statuses := make([]v1alpha1.BackendStatus, 0, len(backends))
	for _, be := range backends {
		_, healthy := healthySet[be.Address]
		message := "unhealthy"
		if healthy {
			message = "healthy"
		}
		statuses = append(statuses, v1alpha1.BackendStatus{
			Address: be.Address,
			Healthy: healthy,
			Message: message,
		})
	}
	return statuses
}

func buildRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
