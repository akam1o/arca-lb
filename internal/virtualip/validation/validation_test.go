package validation

import (
	"strings"
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	commonmodels "github.com/akam1o/arca-lb/internal/common/models"
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

func TestValidateSpecRejectsHealthCheckValuesExceedingInt32(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*v1alpha1.HealthCheckSpec)
		wantErr string
	}{
		{
			name: "interval",
			mutate: func(hc *v1alpha1.HealthCheckSpec) {
				hc.IntervalSeconds = commonmodels.MaxHealthCheckSeconds + 1
			},
			wantErr: "spec.healthCheck.intervalSeconds must be <=",
		},
		{
			name: "timeout",
			mutate: func(hc *v1alpha1.HealthCheckSpec) {
				hc.IntervalSeconds = commonmodels.MaxHealthCheckSeconds
				hc.TimeoutSeconds = commonmodels.MaxHealthCheckSeconds + 1
			},
			wantErr: "spec.healthCheck.timeoutSeconds must be <=",
		},
		{
			name: "rise count",
			mutate: func(hc *v1alpha1.HealthCheckSpec) {
				hc.RiseCount = commonmodels.MaxHealthCheckCount + 1
			},
			wantErr: "spec.healthCheck.riseCount must be <=",
		},
		{
			name: "fall count",
			mutate: func(hc *v1alpha1.HealthCheckSpec) {
				hc.FallCount = commonmodels.MaxHealthCheckCount + 1
			},
			wantErr: "spec.healthCheck.fallCount must be <=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.HealthCheck = validHTTPHealthCheckSpec()
			spec.HealthCheck.RiseCount = 3
			spec.HealthCheck.FallCount = 2
			tt.mutate(spec.HealthCheck)

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
		name           string
		address        string
		encapType      v1alpha1.EncapType
		backendAddress string
	}{
		{
			name:           "NAT4 with IPv4 VIP",
			address:        "203.0.113.10",
			encapType:      v1alpha1.EncapTypeNAT4,
			backendAddress: "10.0.1.1",
		},
		{
			name:           "NAT6 with IPv6 VIP",
			address:        "2001:db8::10",
			encapType:      v1alpha1.EncapTypeNAT6,
			backendAddress: "2001:db8::20",
		},
		{
			name:           "GRE4 with IPv6 VIP",
			address:        "2001:db8::10",
			encapType:      v1alpha1.EncapTypeGRE4,
			backendAddress: "10.0.1.1",
		},
		{
			name:           "GRE6 with IPv4 VIP",
			address:        "203.0.113.10",
			encapType:      v1alpha1.EncapTypeGRE6,
			backendAddress: "2001:db8::20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.Address = tt.address
			spec.EncapType = tt.encapType
			spec.Backends[0].Address = tt.backendAddress

			if err := ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() error = %v", err)
			}
		})
	}
}

func TestValidateSpecRejectsUnsupportedBackendAddressFamilies(t *testing.T) {
	tests := []struct {
		name           string
		vipAddress     string
		encapType      v1alpha1.EncapType
		backendAddress string
		wantErr        string
	}{
		{
			name:           "GRE4 with IPv6 backend",
			vipAddress:     "2001:db8::10",
			encapType:      v1alpha1.EncapTypeGRE4,
			backendAddress: "2001:db8::20",
			wantErr:        `spec.backends[0].address "2001:db8::20" must be IPv4 for encapType "GRE4"`,
		},
		{
			name:           "L3DSR with IPv6 backend",
			vipAddress:     "203.0.113.10",
			encapType:      v1alpha1.EncapTypeL3DSR,
			backendAddress: "2001:db8::20",
			wantErr:        `spec.backends[0].address "2001:db8::20" must be IPv4 for encapType "L3DSR"`,
		},
		{
			name:           "NAT4 with IPv6 backend",
			vipAddress:     "203.0.113.10",
			encapType:      v1alpha1.EncapTypeNAT4,
			backendAddress: "2001:db8::20",
			wantErr:        `spec.backends[0].address "2001:db8::20" must be IPv4 for encapType "NAT4"`,
		},
		{
			name:           "GRE6 with IPv4 backend",
			vipAddress:     "203.0.113.10",
			encapType:      v1alpha1.EncapTypeGRE6,
			backendAddress: "10.0.1.1",
			wantErr:        `spec.backends[0].address "10.0.1.1" must be IPv6 for encapType "GRE6"`,
		},
		{
			name:           "NAT6 with IPv4 backend",
			vipAddress:     "2001:db8::10",
			encapType:      v1alpha1.EncapTypeNAT6,
			backendAddress: "10.0.1.1",
			wantErr:        `spec.backends[0].address "10.0.1.1" must be IPv6 for encapType "NAT6"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.Address = tt.vipAddress
			spec.EncapType = tt.encapType
			spec.Backends[0].Address = tt.backendAddress

			err := ValidateSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateSpec() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSpecAllowsSupportedBackendAddressFamilies(t *testing.T) {
	tests := []struct {
		name           string
		vipAddress     string
		encapType      v1alpha1.EncapType
		backendAddress string
	}{
		{
			name:           "GRE4 with IPv4 backend",
			vipAddress:     "2001:db8::10",
			encapType:      v1alpha1.EncapTypeGRE4,
			backendAddress: "10.0.1.1",
		},
		{
			name:           "GRE6 with IPv6 backend",
			vipAddress:     "203.0.113.10",
			encapType:      v1alpha1.EncapTypeGRE6,
			backendAddress: "2001:db8::20",
		},
		{
			name:           "NAT6 with IPv6 backend",
			vipAddress:     "2001:db8::10",
			encapType:      v1alpha1.EncapTypeNAT6,
			backendAddress: "2001:db8::20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validVirtualIPSpec()
			spec.Address = tt.vipAddress
			spec.EncapType = tt.encapType
			spec.Backends[0].Address = tt.backendAddress

			if err := ValidateSpec(spec); err != nil {
				t.Fatalf("ValidateSpec() error = %v", err)
			}
		})
	}
}
