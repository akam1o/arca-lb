package validation

import (
	"strings"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

func validVirtualIPSpec() *v1alpha1.VirtualIPSpec {
	return &v1alpha1.VirtualIPSpec{
		Address:   "203.0.113.10",
		Port:      80,
		Protocol:  v1alpha1.ProtocolTCP,
		EncapType: v1alpha1.EncapTypeL3DSR,
		Backends: []v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 100},
		},
	}
}

func validHTTPHealthCheckSpec() *v1alpha1.HealthCheckSpec {
	return &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypeHTTP,
		IntervalSeconds: 5,
		TimeoutSeconds:  3,
		HTTP: &v1alpha1.HTTPHealthCheck{
			Port:   8080,
			Path:   "/health?ready=1",
			Method: "GET",
		},
	}
}

func TestValidateSpecAllowsValidHTTPHealthCheckMethodAndPath(t *testing.T) {
	for _, method := range []string{"", "GET", "HEAD", "POST"} {
		t.Run(method, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.HealthCheck = validHTTPHealthCheckSpec()
			spec.HealthCheck.HTTP.Method = method

			if err := ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() error = %v", err)
			}
		})
	}
}

func TestValidateSpecRejectsInvalidHTTPHealthCheckMethodAndPath(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*v1alpha1.HTTPHealthCheck)
		wantErr string
	}{
		{
			name: "unsupported method",
			mutate: func(hc *v1alpha1.HTTPHealthCheck) {
				hc.Method = "TRACE"
			},
			wantErr: "spec.healthCheck.http.method",
		},
		{
			name: "absolute URL path",
			mutate: func(hc *v1alpha1.HTTPHealthCheck) {
				hc.Path = "http://169.254.169.254/latest/meta-data"
			},
			wantErr: "spec.healthCheck.http.path must be relative",
		},
		{
			name: "protocol-relative path",
			mutate: func(hc *v1alpha1.HTTPHealthCheck) {
				hc.Path = "//169.254.169.254/latest/meta-data"
			},
			wantErr: "spec.healthCheck.http.path must be relative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.HealthCheck = validHTTPHealthCheckSpec()
			tt.mutate(spec.HealthCheck.HTTP)

			err := ValidateSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateSpec() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSpecRejectsUnsupportedEncapAddressFamilies(t *testing.T) {
	tests := []struct {
		name      string
		address   string
		encapType v1alpha1.EncapType
		wantErr   string
	}{
		{
			name:      "L3DSR with IPv6 VIP",
			address:   "2001:db8::10",
			encapType: v1alpha1.EncapTypeL3DSR,
			wantErr:   `spec.encapType "L3DSR" requires an IPv4 spec.address`,
		},
		{
			name:      "NAT4 with IPv6 VIP",
			address:   "2001:db8::10",
			encapType: v1alpha1.EncapTypeNAT4,
			wantErr:   `spec.encapType "NAT4" requires an IPv4 spec.address`,
		},
		{
			name:      "NAT6 with IPv4 VIP",
			address:   "203.0.113.10",
			encapType: v1alpha1.EncapTypeNAT6,
			wantErr:   `spec.encapType "NAT6" requires an IPv6 spec.address`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.Address = tt.address
			spec.EncapType = tt.encapType

			err := ValidateSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateSpec() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSpecAllowsSupportedEncapAddressFamilies(t *testing.T) {
	tests := []struct {
		name      string
		address   string
		encapType v1alpha1.EncapType
	}{
		{
			name:      "NAT4 with IPv4 VIP",
			address:   "203.0.113.10",
			encapType: v1alpha1.EncapTypeNAT4,
		},
		{
			name:      "NAT6 with IPv6 VIP",
			address:   "2001:db8::10",
			encapType: v1alpha1.EncapTypeNAT6,
		},
		{
			name:      "GRE4 with IPv6 VIP",
			address:   "2001:db8::10",
			encapType: v1alpha1.EncapTypeGRE4,
		},
		{
			name:      "GRE6 with IPv4 VIP",
			address:   "203.0.113.10",
			encapType: v1alpha1.EncapTypeGRE6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.Address = tt.address
			spec.EncapType = tt.encapType

			if err := ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() error = %v", err)
			}
		})
	}
}
