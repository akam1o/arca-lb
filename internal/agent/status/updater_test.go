package status

import (
	"context"
	"io"
	"log/slog"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"

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
