// Package status updates Kubernetes VirtualIP status from agent observations.
package status

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ConditionHealthCheckReady reports whether the agent accepted the current
	// health check configuration.
	ConditionHealthCheckReady = "HealthCheckReady"

	// ConditionServing reports whether this VirtualIP address:port/protocol has
	// at least one backend that can currently receive traffic on this node.
	ConditionServing = "Serving"

	// ConditionRouteAdvertised reports whether the shared VIP address route is
	// currently advertised by this node.
	ConditionRouteAdvertised = "RouteAdvertised"
)

// Config configures Kubernetes API access for status updates.
type Config struct {
	// Kubeconfig path. Empty uses in-cluster config.
	Kubeconfig string

	// AgentID identifies this agent in per-agent status, typically the node name.
	AgentID string
}

// Updater writes agent-observed backend health to VirtualIP status.
type Updater struct {
	client  client.Client
	agentID string
	logger  *slog.Logger
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
		client:  k8sClient,
		agentID: normalizeAgentID(cfg.AgentID),
		logger:  logger.With("component", "status-updater"),
	}, nil
}

// UpdateVIPStatus updates backend health fields in the VirtualIP status
// subresource and optionally records additional agent-owned conditions.
func (u *Updater) UpdateVIPStatus(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec, conditions ...metav1.Condition) error {
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

		agentID := normalizeAgentID(u.agentID)
		now := metav1.Now()
		agentStatus := v1alpha1.AgentStatus{
			AgentID:            agentID,
			ObservedGeneration: vip.Generation,
			TotalBackends:      len(vip.Spec.Backends),
			HealthyBackends:    len(healthyBackends),
			Backends:           buildBackendStatuses(vip.Spec.Backends, healthySet),
			LastUpdateTime:     &now,
		}
		for _, condition := range conditions {
			condition.ObservedGeneration = vip.Generation
			meta.SetStatusCondition(&agentStatus.Conditions, condition)
		}

		current.Status.AgentStatuses = upsertAgentStatus(
			current.Status.AgentStatuses, agentStatus, vip.Generation,
		)
		applyAggregateStatus(&current, vip.Generation)

		return u.client.Status().Update(ctx, &current)
	})
}

// UpdateHealthCheckCondition records whether the agent accepted the health check config.
func (u *Updater) UpdateHealthCheckCondition(ctx context.Context, vip *v1alpha1.VirtualIP, condition metav1.Condition) error {
	if u == nil || u.client == nil || vip == nil {
		return nil
	}

	key := types.NamespacedName{Namespace: vip.Namespace, Name: vip.Name}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current v1alpha1.VirtualIP
		if err := u.client.Get(ctx, key, &current); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if current.UID != vip.UID || current.Generation != vip.Generation || !current.DeletionTimestamp.IsZero() {
			u.logger.Debug("skipping stale VirtualIP health check condition update",
				"namespace", vip.Namespace,
				"name", vip.Name,
				"observed_generation", vip.Generation,
				"current_generation", current.Generation)
			return nil
		}

		condition.Type = ConditionHealthCheckReady
		condition.ObservedGeneration = vip.Generation
		meta.SetStatusCondition(&current.Status.Conditions, condition)

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

func normalizeAgentID(agentID string) string {
	if agentID != "" {
		return agentID
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}
	return "unknown"
}

func upsertAgentStatus(statuses []v1alpha1.AgentStatus, next v1alpha1.AgentStatus, generation int64) []v1alpha1.AgentStatus {
	out := make([]v1alpha1.AgentStatus, 0, len(statuses)+1)
	replaced := false
	for _, status := range statuses {
		if status.AgentID == next.AgentID {
			out = append(out, next)
			replaced = true
			continue
		}
		if status.ObservedGeneration == generation {
			out = append(out, status)
		}
	}
	if !replaced {
		out = append(out, next)
	}
	return out
}

func applyAggregateStatus(vip *v1alpha1.VirtualIP, generation int64) {
	currentStatuses := make([]v1alpha1.AgentStatus, 0, len(vip.Status.AgentStatuses))
	for _, status := range vip.Status.AgentStatuses {
		if status.ObservedGeneration == generation {
			currentStatuses = append(currentStatuses, status)
		}
	}

	healthySet := make(map[string]struct{})
	for _, status := range currentStatuses {
		for _, backend := range status.Backends {
			if backend.Healthy {
				healthySet[backend.Address] = struct{}{}
			}
		}
	}

	vip.Status.ObservedGeneration = generation
	vip.Status.TotalBackends = len(vip.Spec.Backends)
	vip.Status.HealthyBackends = len(healthySet)
	vip.Status.Backends = buildBackendStatuses(vip.Spec.Backends, healthySet)

	conditions := preserveNonAgentConditions(vip.Status.Conditions)
	meta.SetStatusCondition(&conditions, aggregateServingCondition(vip, currentStatuses))
	meta.SetStatusCondition(&conditions, aggregateRouteAdvertisedCondition(vip, currentStatuses))
	vip.Status.Conditions = conditions
}

func preserveNonAgentConditions(conditions []metav1.Condition) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		switch condition.Type {
		case ConditionServing, ConditionRouteAdvertised:
			continue
		default:
			out = append(out, condition)
		}
	}
	return out
}

func aggregateServingCondition(vip *v1alpha1.VirtualIP, statuses []v1alpha1.AgentStatus) metav1.Condition {
	condition := metav1.Condition{
		Type:               ConditionServing,
		ObservedGeneration: vip.Generation,
	}
	if len(statuses) == 0 {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "NoAgentStatus"
		condition.Message = "No agent has reported serving state for this generation"
		return condition
	}

	var unknown *metav1.Condition
	for _, status := range statuses {
		serving := meta.FindStatusCondition(status.Conditions, ConditionServing)
		if serving == nil {
			if unknown == nil {
				unknown = &metav1.Condition{
					Status:  metav1.ConditionUnknown,
					Reason:  "MissingAgentCondition",
					Message: fmt.Sprintf("Agent %s has not reported serving state", status.AgentID),
				}
			}
			continue
		}
		if serving.Status == metav1.ConditionTrue {
			condition.Status = metav1.ConditionTrue
			condition.Reason = "AgentServing"
			condition.Message = fmt.Sprintf("At least one agent is serving %s:%d/%s", vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
			return condition
		}
		if serving.Status == metav1.ConditionUnknown && unknown == nil {
			copy := *serving
			unknown = &copy
		}
	}
	if unknown != nil {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = unknown.Reason
		condition.Message = unknown.Message
		return condition
	}

	condition.Status = metav1.ConditionFalse
	if len(vip.Spec.Backends) == 0 {
		condition.Reason = "NoBackends"
		condition.Message = fmt.Sprintf("No backends configured for %s:%d/%s",
			vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
		return condition
	}
	condition.Reason = "NoAgentServing"
	condition.Message = fmt.Sprintf("No agent is serving %s:%d/%s", vip.Spec.Address, vip.Spec.Port, vip.Spec.Protocol)
	return condition
}

func aggregateRouteAdvertisedCondition(vip *v1alpha1.VirtualIP, statuses []v1alpha1.AgentStatus) metav1.Condition {
	condition := metav1.Condition{
		Type:               ConditionRouteAdvertised,
		ObservedGeneration: vip.Generation,
	}
	if len(statuses) == 0 {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "NoAgentStatus"
		condition.Message = "No agent has reported route advertisement state for this generation"
		return condition
	}

	var unknown *metav1.Condition
	advertised := 0
	for _, status := range statuses {
		route := meta.FindStatusCondition(status.Conditions, ConditionRouteAdvertised)
		if route == nil {
			if unknown == nil {
				unknown = &metav1.Condition{
					Status:  metav1.ConditionUnknown,
					Reason:  "MissingAgentCondition",
					Message: fmt.Sprintf("Agent %s has not reported route advertisement state", status.AgentID),
				}
			}
			continue
		}
		switch route.Status {
		case metav1.ConditionTrue:
			advertised++
		case metav1.ConditionUnknown:
			if unknown == nil {
				copy := *route
				unknown = &copy
			}
		}
	}

	if unknown != nil {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = unknown.Reason
		condition.Message = unknown.Message
		return condition
	}
	if advertised > 0 {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Advertised"
		condition.Message = fmt.Sprintf("VIP address %s is advertised by %d agent(s)", vip.Spec.Address, advertised)
		return condition
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = "NotAdvertised"
	condition.Message = fmt.Sprintf("VIP address %s is not advertised by any reporting agent", vip.Spec.Address)
	return condition
}

func buildRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
