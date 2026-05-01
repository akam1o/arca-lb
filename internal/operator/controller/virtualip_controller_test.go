package controller

import (
	"context"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func validVirtualIPSpec() v1alpha1.VirtualIPSpec {
	return v1alpha1.VirtualIPSpec{
		Address:   "203.0.113.10",
		Port:      80,
		Protocol:  v1alpha1.ProtocolTCP,
		EncapType: v1alpha1.EncapTypeL3DSR,
		Backends: []v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 100},
		},
	}
}

func TestValidateSpecL3DSRAllowsMissingDSCP(t *testing.T) {
	spec := validVirtualIPSpec()

	if err := validateSpec(&spec); err != nil {
		t.Fatalf("validateSpec rejected L3DSR without per-VIP DSCP: %v", err)
	}
}

func TestValidateSpecL3DSRRejectsInvalidDSCPOverride(t *testing.T) {
	dscp := uint8(0)
	spec := validVirtualIPSpec()
	spec.DSCP = &dscp

	if err := validateSpec(&spec); err == nil {
		t.Fatal("expected invalid DSCP override to be rejected")
	}
}

func TestApplyDefaultsUsesBackendWeightOne(t *testing.T) {
	vip := &v1alpha1.VirtualIP{
		Spec: v1alpha1.VirtualIPSpec{
			Address:  "203.0.113.10",
			Port:     80,
			Protocol: v1alpha1.ProtocolTCP,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.1.1"},
			},
		},
	}

	if changed := applyDefaults(vip); !changed {
		t.Fatal("expected defaults to be applied")
	}
	if got := vip.Spec.Backends[0].Weight; got != v1alpha1.DefaultBackendWeight {
		t.Fatalf("backend weight default = %d, want %d", got, v1alpha1.DefaultBackendWeight)
	}
}

func TestUpdateStatusDoesNotAdvanceAgentObservedGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	vip := &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "web",
			UID:        types.UID("vip-1"),
			Generation: 7,
		},
		Spec: v1alpha1.VirtualIPSpec{
			Address:   "203.0.113.10",
			Port:      80,
			Protocol:  v1alpha1.ProtocolTCP,
			EncapType: v1alpha1.EncapTypeL3DSR,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.1.1", Weight: 100},
				{Address: "10.0.1.2", Weight: 100},
			},
		},
		Status: v1alpha1.VirtualIPStatus{
			ObservedGeneration: 6,
			HealthyBackends:    1,
			TotalBackends:      1,
			Backends: []v1alpha1.BackendStatus{
				{Address: "10.0.1.1", Healthy: true},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.VirtualIP{}).
		WithObjects(vip).
		Build()
	reconciler := &VirtualIPReconciler{
		Client: k8sClient,
		Scheme: scheme,
	}

	if err := reconciler.updateStatus(context.Background(), vip); err != nil {
		t.Fatalf("updateStatus: %v", err)
	}

	var got v1alpha1.VirtualIP
	key := types.NamespacedName{Namespace: "default", Name: "web"}
	if err := k8sClient.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Status.ObservedGeneration != 6 {
		t.Fatalf("ObservedGeneration = %d, want agent-observed generation 6", got.Status.ObservedGeneration)
	}
	if got.Status.HealthyBackends != 1 {
		t.Fatalf("HealthyBackends = %d, want preserved agent value 1", got.Status.HealthyBackends)
	}
	if len(got.Status.Backends) != 1 || got.Status.Backends[0].Address != "10.0.1.1" {
		t.Fatalf("Backends = %#v, want preserved agent backend status", got.Status.Backends)
	}
	if got.Status.TotalBackends != 2 {
		t.Fatalf("TotalBackends = %d, want current spec count 2", got.Status.TotalBackends)
	}

	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("Ready condition was not set")
	}
	if ready.ObservedGeneration != 7 {
		t.Fatalf("Ready observedGeneration = %d, want current generation 7", ready.ObservedGeneration)
	}
	if ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready status = %s, want True", ready.Status)
	}
}

func TestValidateSpecAllowsValidHealthChecks(t *testing.T) {
	tests := []struct {
		name string
		hc   *v1alpha1.HealthCheckSpec
	}{
		{
			name: "http",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeHTTP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
				HTTP: &v1alpha1.HTTPHealthCheck{
					Port: 8080,
					Path: "/healthz",
				},
			},
		},
		{
			name: "tcp",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeTCP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
				TCP: &v1alpha1.TCPHealthCheck{
					Port: 8443,
				},
			},
		},
		{
			name: "tls-hello",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeTLSHello,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
				TCP: &v1alpha1.TCPHealthCheck{
					Port: 8443,
				},
			},
		},
		{
			name: "ping",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypePing,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.HealthCheck = tt.hc

			if err := validateSpec(&spec); err != nil {
				t.Fatalf("validateSpec rejected valid healthCheck: %v", err)
			}
		})
	}
}

func TestValidateSpecRejectsInvalidHealthChecks(t *testing.T) {
	tests := []struct {
		name string
		hc   *v1alpha1.HealthCheckSpec
	}{
		{
			name: "http missing config",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeHTTP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
			},
		},
		{
			name: "http port out of range",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeHTTP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
				HTTP: &v1alpha1.HTTPHealthCheck{
					Port: 0,
				},
			},
		},
		{
			name: "tcp missing config",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeTCP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
			},
		},
		{
			name: "tls-hello missing config",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeTLSHello,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
			},
		},
		{
			name: "tcp port out of range",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypeTCP,
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
				TCP: &v1alpha1.TCPHealthCheck{
					Port: 70000,
				},
			},
		},
		{
			name: "invalid type",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCType("smtp"),
				IntervalSeconds: 5,
				TimeoutSeconds:  3,
			},
		},
		{
			name: "timeout equals interval",
			hc: &v1alpha1.HealthCheckSpec{
				Type:            v1alpha1.HCTypePing,
				IntervalSeconds: 3,
				TimeoutSeconds:  3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.HealthCheck = tt.hc

			if err := validateSpec(&spec); err == nil {
				t.Fatal("expected invalid healthCheck to be rejected")
			}
		})
	}
}
