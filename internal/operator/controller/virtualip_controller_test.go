package controller

import (
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
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
