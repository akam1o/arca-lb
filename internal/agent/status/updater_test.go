package status

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

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
		client:  k8sClient,
		agentID: "node-a",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	if ready := meta.FindStatusCondition(got.Status.Conditions, "Ready"); ready == nil {
		t.Fatalf("Ready condition was not preserved: %+v", got.Status.Conditions)
	}
	if len(got.Status.AgentStatuses) != 1 {
		t.Fatalf("AgentStatuses = %d, want 1", len(got.Status.AgentStatuses))
	}
	if got.Status.AgentStatuses[0].AgentID != "node-a" {
		t.Fatalf("AgentStatus agentID = %q, want node-a", got.Status.AgentStatuses[0].AgentID)
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
		client:  k8sClient,
		agentID: "node-a",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
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

func TestUpdateVIPStatusAggregatesPerAgentStatus(t *testing.T) {
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
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
				{Address: "10.0.0.2", Weight: 100},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(vip).
		Build()
	nodeA := &Updater{
		client:  k8sClient,
		agentID: "node-a",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	nodeB := &Updater{
		client:  k8sClient,
		agentID: "node-b",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := nodeA.UpdateVIPStatus(context.Background(), vip, []v1alpha1.BackendSpec{
		{Address: "10.0.0.1", Weight: 100},
	}, metav1.Condition{
		Type:    ConditionServing,
		Status:  metav1.ConditionTrue,
		Reason:  "BackendsHealthy",
		Message: "node-a serving",
	}, metav1.Condition{
		Type:    ConditionRouteAdvertised,
		Status:  metav1.ConditionTrue,
		Reason:  "Advertised",
		Message: "node-a advertised",
	}); err != nil {
		t.Fatalf("nodeA UpdateVIPStatus: %v", err)
	}
	if err := nodeB.UpdateVIPStatus(context.Background(), vip, []v1alpha1.BackendSpec{
		{Address: "10.0.0.2", Weight: 100},
	}, metav1.Condition{
		Type:    ConditionServing,
		Status:  metav1.ConditionFalse,
		Reason:  "NoHealthyBackends",
		Message: "node-b not serving",
	}, metav1.Condition{
		Type:    ConditionRouteAdvertised,
		Status:  metav1.ConditionUnknown,
		Reason:  "RouteUpdateFailed",
		Message: "vtysh failed",
	}); err != nil {
		t.Fatalf("nodeB UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Status.AgentStatuses) != 2 {
		t.Fatalf("AgentStatuses = %d, want 2", len(got.Status.AgentStatuses))
	}
	if got.Status.HealthyBackends != 2 {
		t.Fatalf("HealthyBackends = %d, want aggregate of 2", got.Status.HealthyBackends)
	}
	serving := meta.FindStatusCondition(got.Status.Conditions, ConditionServing)
	if serving == nil || serving.Status != metav1.ConditionTrue {
		t.Fatalf("Serving aggregate = %+v, want True", serving)
	}
	advertised := meta.FindStatusCondition(got.Status.Conditions, ConditionRouteAdvertised)
	if advertised == nil || advertised.Status != metav1.ConditionUnknown || advertised.Reason != "RouteUpdateFailed" {
		t.Fatalf("RouteAdvertised aggregate = %+v, want Unknown RouteUpdateFailed", advertised)
	}
}

func TestUpdateVIPStatusTreatsExpiredAgentStatusAsUnknown(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	expired := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 3,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
			},
		},
		Status: v1alpha1.VirtualIPStatus{
			ObservedGeneration: 3,
			HealthyBackends:    1,
			TotalBackends:      1,
			Backends: []v1alpha1.BackendStatus{
				{Address: "10.0.0.1", Healthy: true},
			},
			AgentStatuses: []v1alpha1.AgentStatus{
				{
					AgentID:            "node-a",
					ObservedGeneration: 3,
					HealthyBackends:    1,
					TotalBackends:      1,
					LastUpdateTime:     &expired,
					Backends: []v1alpha1.BackendStatus{
						{Address: "10.0.0.1", Healthy: true},
					},
					Conditions: []metav1.Condition{
						{Type: ConditionServing, Status: metav1.ConditionTrue, Reason: "BackendsHealthy"},
						{Type: ConditionRouteAdvertised, Status: metav1.ConditionTrue, Reason: "Advertised"},
					},
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
		client:         k8sClient,
		agentID:        "node-b",
		agentStatusTTL: time.Minute,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateVIPStatus(context.Background(), vip, nil, metav1.Condition{
		Type:    ConditionServing,
		Status:  metav1.ConditionFalse,
		Reason:  "NoHealthyBackends",
		Message: "node-b not serving",
	}, metav1.Condition{
		Type:    ConditionRouteAdvertised,
		Status:  metav1.ConditionFalse,
		Reason:  "NotAdvertised",
		Message: "node-b not advertised",
	}); err != nil {
		t.Fatalf("UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Status.AgentStatuses) != 2 {
		t.Fatalf("AgentStatuses = %#v, want fresh and expired current-generation statuses retained", got.Status.AgentStatuses)
	}
	if got.Status.HealthyBackends != 0 {
		t.Fatalf("HealthyBackends = %d, want expired node-a health excluded", got.Status.HealthyBackends)
	}
	serving := meta.FindStatusCondition(got.Status.Conditions, ConditionServing)
	if serving == nil || serving.Status != metav1.ConditionUnknown || serving.Reason != reasonAgentStatusExpired {
		t.Fatalf("Serving aggregate = %+v, want Unknown AgentStatusExpired", serving)
	}
	advertised := meta.FindStatusCondition(got.Status.Conditions, ConditionRouteAdvertised)
	if advertised == nil || advertised.Status != metav1.ConditionUnknown || advertised.Reason != reasonAgentStatusExpired {
		t.Fatalf("RouteAdvertised aggregate = %+v, want Unknown AgentStatusExpired", advertised)
	}
}

func TestUpdateVIPStatusPrunesExpiredAgentStatusAfterRetention(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	old := metav1.NewTime(time.Now().Add(-DefaultExpiredAgentStatusRetention - time.Minute))
	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 3,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.0.1", Weight: 100},
			},
		},
		Status: v1alpha1.VirtualIPStatus{
			ObservedGeneration: 3,
			HealthyBackends:    1,
			TotalBackends:      1,
			Backends: []v1alpha1.BackendStatus{
				{Address: "10.0.0.1", Healthy: true},
			},
			AgentStatuses: []v1alpha1.AgentStatus{
				{
					AgentID:            "node-a",
					ObservedGeneration: 3,
					HealthyBackends:    1,
					TotalBackends:      1,
					LastUpdateTime:     &old,
					Backends: []v1alpha1.BackendStatus{
						{Address: "10.0.0.1", Healthy: true},
					},
					Conditions: []metav1.Condition{
						{Type: ConditionServing, Status: metav1.ConditionTrue, Reason: "BackendsHealthy"},
						{Type: ConditionRouteAdvertised, Status: metav1.ConditionTrue, Reason: "Advertised"},
					},
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
		client:         k8sClient,
		agentID:        "node-b",
		agentStatusTTL: time.Minute,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := updater.UpdateVIPStatus(context.Background(), vip, nil, metav1.Condition{
		Type:    ConditionServing,
		Status:  metav1.ConditionFalse,
		Reason:  "NoHealthyBackends",
		Message: "node-b not serving",
	}, metav1.Condition{
		Type:    ConditionRouteAdvertised,
		Status:  metav1.ConditionFalse,
		Reason:  "NotAdvertised",
		Message: "node-b not advertised",
	}); err != nil {
		t.Fatalf("UpdateVIPStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "web"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Status.AgentStatuses) != 1 || got.Status.AgentStatuses[0].AgentID != "node-b" {
		t.Fatalf("AgentStatuses = %#v, want only fresh node-b status", got.Status.AgentStatuses)
	}
	if got.Status.HealthyBackends != 0 {
		t.Fatalf("HealthyBackends = %d, want pruned node-a health excluded", got.Status.HealthyBackends)
	}
	serving := meta.FindStatusCondition(got.Status.Conditions, ConditionServing)
	if serving == nil || serving.Status != metav1.ConditionFalse || serving.Reason != "NoAgentServing" {
		t.Fatalf("Serving aggregate = %+v, want False NoAgentServing", serving)
	}
	advertised := meta.FindStatusCondition(got.Status.Conditions, ConditionRouteAdvertised)
	if advertised == nil || advertised.Status != metav1.ConditionFalse || advertised.Reason != "NotAdvertised" {
		t.Fatalf("RouteAdvertised aggregate = %+v, want False NotAdvertised", advertised)
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
		client:  k8sClient,
		agentID: "node-a",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
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
		client:  k8sClient,
		agentID: "node-a",
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
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
