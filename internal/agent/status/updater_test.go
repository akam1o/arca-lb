package status

import (
	"context"
	"io"
	"log/slog"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateVIPStatusWritesHealthAndPreservesConditions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 3,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
				{Address: "10.0.0.2", Weight: 100},
			},
		},
		Status: v1alpha1.VirtualIPStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "Configured",
					ObservedGeneration: 3,
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(vip).
		Build()
	updater := &Updater{
		client: k8sClient,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateVIPStatus(context.Background(), vip, []v1alpha1.BackendSpec{
		{Address: "10.0.0.2", Weight: 100},
	}); err != nil {
		t.Fatalf("UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Status.ObservedGeneration != 3 {
		t.Fatalf("ObservedGeneration = %d, want 3", got.Status.ObservedGeneration)
	}
	if got.Status.TotalBackends != 2 {
		t.Fatalf("TotalBackends = %d, want 2", got.Status.TotalBackends)
	}
	if got.Status.HealthyBackends != 1 {
		t.Fatalf("HealthyBackends = %d, want 1", got.Status.HealthyBackends)
	}
	if len(got.Status.Backends) != 2 {
		t.Fatalf("Backends = %d, want 2", len(got.Status.Backends))
	}
	if got.Status.Backends[0].Address != "10.0.0.1" || got.Status.Backends[0].Healthy {
		t.Fatalf("first backend status = %+v, want unhealthy 10.0.0.1", got.Status.Backends[0])
	}
	if got.Status.Backends[1].Address != "10.0.0.2" || !got.Status.Backends[1].Healthy {
		t.Fatalf("second backend status = %+v, want healthy 10.0.0.2", got.Status.Backends[1])
	}
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Type != "Ready" {
		t.Fatalf("conditions were not preserved: %+v", got.Status.Conditions)
	}
}

func TestUpdateVIPStatusWritesAgentConditions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 3,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(vip).
		Build()
	updater := &Updater{
		client: k8sClient,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateVIPStatus(context.Background(), vip, []v1alpha1.BackendSpec{
		{Address: "10.0.0.1", Weight: 100},
	}, metav1.Condition{
		Type:    ConditionServing,
		Status:  metav1.ConditionTrue,
		Reason:  "BackendsHealthy",
		Message: "1 healthy backend available",
	}, metav1.Condition{
		Type:    ConditionRouteAdvertised,
		Status:  metav1.ConditionTrue,
		Reason:  "Advertised",
		Message: "VIP address is advertised",
	}); err != nil {
		t.Fatalf("UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	serving := meta.FindStatusCondition(got.Status.Conditions, ConditionServing)
	if serving == nil || serving.Status != metav1.ConditionTrue || serving.ObservedGeneration != 3 {
		t.Fatalf("Serving condition = %+v, want True at generation 3", serving)
	}
	advertised := meta.FindStatusCondition(got.Status.Conditions, ConditionRouteAdvertised)
	if advertised == nil || advertised.Status != metav1.ConditionTrue || advertised.ObservedGeneration != 3 {
		t.Fatalf("RouteAdvertised condition = %+v, want True at generation 3", advertised)
	}
}

func TestUpdateVIPStatusSkipsStaleGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	current := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 4,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Backends: []v1alpha1.BackendSpec{{Address: "10.0.0.1", Weight: 100}},
		},
	}
	stale := current.DeepCopy()
	stale.Generation = 3

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(current).
		Build()
	updater := &Updater{
		client: k8sClient,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateVIPStatus(context.Background(), stale, []v1alpha1.BackendSpec{
		{Address: "10.0.0.1", Weight: 100},
	}); err != nil {
		t.Fatalf("UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.HealthyBackends != 0 {
		t.Fatalf("HealthyBackends = %d, want stale update to be skipped", got.Status.HealthyBackends)
	}
}

func TestUpdateHealthCheckCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 3,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(vip).
		Build()
	updater := &Updater{
		client: k8sClient,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateHealthCheckCondition(context.Background(), vip, metav1.Condition{
		Status:  metav1.ConditionFalse,
		Reason:  "InvalidHealthCheck",
		Message: "HTTP health check config is required",
	}); err != nil {
		t.Fatalf("UpdateHealthCheckCondition: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Status.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1", len(got.Status.Conditions))
	}
	condition := got.Status.Conditions[0]
	if condition.Type != ConditionHealthCheckReady {
		t.Fatalf("condition type = %q, want %q", condition.Type, ConditionHealthCheckReady)
	}
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("condition status = %s, want False", condition.Status)
	}
	if condition.Reason != "InvalidHealthCheck" {
		t.Fatalf("condition reason = %q, want InvalidHealthCheck", condition.Reason)
	}
	if condition.ObservedGeneration != 3 {
		t.Fatalf("condition observedGeneration = %d, want 3", condition.ObservedGeneration)
	}
}
