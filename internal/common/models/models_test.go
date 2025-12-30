package models

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
}

func TestVIP_Validation(t *testing.T) {
	tests := []struct {
		name        string
		vip         VIP
		wantError   bool
		errorFields []string
	}{
		{
			name: "valid VIP",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     80,
				Protocol: ProtocolTCP,
				LBMethod: LBMethodMaglev,
			},
			wantError: false,
		},
		{
			name: "missing VIP",
			vip: VIP{
				Port:     80,
				Protocol: ProtocolTCP,
			},
			wantError:   true,
			errorFields: []string{"VIP"},
		},
		{
			name: "invalid VIP format",
			vip: VIP{
				VIP:      "invalid-ip",
				Port:     80,
				Protocol: ProtocolTCP,
			},
			wantError:   true,
			errorFields: []string{"VIP"},
		},
		{
			name: "missing port",
			vip: VIP{
				VIP:      "192.168.1.100",
				Protocol: ProtocolTCP,
			},
			wantError:   true,
			errorFields: []string{"Port"},
		},
		{
			name: "port too low",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     0,
				Protocol: ProtocolTCP,
			},
			wantError:   true,
			errorFields: []string{"Port"},
		},
		{
			name: "port too high",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     65536,
				Protocol: ProtocolTCP,
			},
			wantError:   true,
			errorFields: []string{"Port"},
		},
		{
			name: "missing protocol",
			vip: VIP{
				VIP:  "192.168.1.100",
				Port: 80,
			},
			wantError:   true,
			errorFields: []string{"Protocol"},
		},
		{
			name: "invalid protocol",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     80,
				Protocol: "INVALID",
			},
			wantError:   true,
			errorFields: []string{"Protocol"},
		},
		{
			name: "valid UDP protocol",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     53,
				Protocol: ProtocolUDP,
			},
			wantError: false,
		},
		{
			name: "invalid LB method",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     80,
				Protocol: ProtocolTCP,
				LBMethod: "invalid_method",
			},
			wantError:   true,
			errorFields: []string{"LBMethod"},
		},
		{
			name: "valid LB method",
			vip: VIP{
				VIP:      "192.168.1.100",
				Port:     80,
				Protocol: ProtocolTCP,
				LBMethod: LBMethodMaglev,
			},
			wantError: false,
		},
		{
			name: "valid IPv6 VIP",
			vip: VIP{
				VIP:      "2001:db8::1",
				Port:     80,
				Protocol: ProtocolTCP,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.vip)
			if tt.wantError {
				require.Error(t, err)
				if len(tt.errorFields) > 0 {
					for _, field := range tt.errorFields {
						assert.Contains(t, err.Error(), field, "Error should mention field: %s", field)
					}
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBackend_Validation(t *testing.T) {
	tests := []struct {
		name        string
		backend     Backend
		wantError   bool
		errorFields []string
	}{
		{
			name: "valid backend",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "10.0.0.1",
				Weight: 10,
			},
			wantError: false,
		},
		{
			name: "missing VIP ID",
			backend: Backend{
				IP:     "10.0.0.1",
				Weight: 10,
			},
			wantError:   true,
			errorFields: []string{"VIPID"},
		},
		{
			name: "missing IP",
			backend: Backend{
				VIPID:  "vip-1",
				Weight: 10,
			},
			wantError:   true,
			errorFields: []string{"IP"},
		},
		{
			name: "invalid IP format",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "invalid-ip",
				Weight: 10,
			},
			wantError:   true,
			errorFields: []string{"IP"},
		},
		{
			name: "missing weight",
			backend: Backend{
				VIPID: "vip-1",
				IP:    "10.0.0.1",
			},
			wantError:   true,
			errorFields: []string{"Weight"},
		},
		{
			name: "weight too low",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "10.0.0.1",
				Weight: 0,
			},
			wantError:   true,
			errorFields: []string{"Weight"},
		},
		{
			name: "weight too high",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "10.0.0.1",
				Weight: 101,
			},
			wantError:   true,
			errorFields: []string{"Weight"},
		},
		{
			name: "weight at minimum",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "10.0.0.1",
				Weight: 1,
			},
			wantError: false,
		},
		{
			name: "weight at maximum",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "10.0.0.1",
				Weight: 100,
			},
			wantError: false,
		},
		{
			name: "valid IPv6 backend",
			backend: Backend{
				VIPID:  "vip-1",
				IP:     "2001:db8::1",
				Weight: 10,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.backend)
			if tt.wantError {
				require.Error(t, err)
				if len(tt.errorFields) > 0 {
					for _, field := range tt.errorFields {
						assert.Contains(t, err.Error(), field, "Error should mention field: %s", field)
					}
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthCheck_Validation(t *testing.T) {
	tests := []struct {
		name        string
		healthCheck HealthCheck
		wantError   bool
		errorFields []string
	}{
		{
			name: "valid HTTP health check",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError: false,
		},
		{
			name: "valid HTTPS health check",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTPS,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError: false,
		},
		{
			name: "valid TCP health check",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeTCP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError: false,
		},
		{
			name: "valid Ping health check",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypePing,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError: false,
		},
		{
			name: "missing VIP ID",
			healthCheck: HealthCheck{
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"VIPID"},
		},
		{
			name: "missing type",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"Type"},
		},
		{
			name: "invalid type",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        "invalid",
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"Type"},
		},
		{
			name: "missing interval",
			healthCheck: HealthCheck{
				VIPID:      "vip-1",
				Type:       HCTypeHTTP,
				TimeoutSec: 5,
				RiseCount:  3,
				FallCount:  3,
			},
			wantError:   true,
			errorFields: []string{"IntervalSec"},
		},
		{
			name: "interval too low",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 0,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"IntervalSec"},
		},
		{
			name: "missing timeout",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"TimeoutSec"},
		},
		{
			name: "timeout too low",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  0,
				RiseCount:   3,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"TimeoutSec"},
		},
		{
			name: "missing rise count",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"RiseCount"},
		},
		{
			name: "rise count too low",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   0,
				FallCount:   3,
			},
			wantError:   true,
			errorFields: []string{"RiseCount"},
		},
		{
			name: "missing fall count",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
			},
			wantError:   true,
			errorFields: []string{"FallCount"},
		},
		{
			name: "fall count too low",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 10,
				TimeoutSec:  5,
				RiseCount:   3,
				FallCount:   0,
			},
			wantError:   true,
			errorFields: []string{"FallCount"},
		},
		{
			name: "minimum valid values",
			healthCheck: HealthCheck{
				VIPID:       "vip-1",
				Type:        HCTypeHTTP,
				IntervalSec: 1,
				TimeoutSec:  1,
				RiseCount:   1,
				FallCount:   1,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.healthCheck)
			if tt.wantError {
				require.Error(t, err)
				if len(tt.errorFields) > 0 {
					for _, field := range tt.errorFields {
						assert.Contains(t, err.Error(), field, "Error should mention field: %s", field)
					}
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestProtocol_Constants(t *testing.T) {
	assert.Equal(t, Protocol("TCP"), ProtocolTCP)
	assert.Equal(t, Protocol("UDP"), ProtocolUDP)
}

func TestLBMethod_Constants(t *testing.T) {
	assert.Equal(t, LBMethod("maglev"), LBMethodMaglev)
}

func TestHCType_Constants(t *testing.T) {
	assert.Equal(t, HCType("http"), HCTypeHTTP)
	assert.Equal(t, HCType("https"), HCTypeHTTPS)
	assert.Equal(t, HCType("tcp"), HCTypeTCP)
	assert.Equal(t, HCType("ping"), HCTypePing)
}
