package models

import (
	"strings"
	"testing"
)

func TestValidateHealthCheckConfig(t *testing.T) {
	tests := []struct {
		name    string
		hcType  HCType
		config  HCConfig
		wantErr string
	}{
		{
			name:   "http accepts valid config",
			hcType: HCTypeHTTP,
			config: HCConfig{
				"port":            float64(8080),
				"path":            "/health",
				"method":          "GET",
				"host_header":     "example.test",
				"tls_skip_verify": true,
				"expected_codes":  []interface{}{float64(200), float64(204)},
				"headers": map[string]interface{}{
					"X-Health": "true",
				},
			},
		},
		{
			name:    "http requires config",
			hcType:  HCTypeHTTP,
			wantErr: "config is required",
		},
		{
			name:   "http rejects fractional port",
			hcType: HCTypeHTTP,
			config: HCConfig{
				"port": float64(8080.5),
			},
			wantErr: "port must be an integer between 1 and 65535",
		},
		{
			name:   "http rejects expected code below range",
			hcType: HCTypeHTTP,
			config: HCConfig{
				"port":           8080,
				"expected_codes": []interface{}{float64(99)},
			},
			wantErr: "expected_codes must be integers between 100 and 599",
		},
		{
			name:   "http rejects non-string header value",
			hcType: HCTypeHTTP,
			config: HCConfig{
				"port": 8080,
				"headers": map[string]interface{}{
					"X-Health": float64(1),
				},
			},
			wantErr: "headers values must be strings",
		},
		{
			name:   "tcp accepts valid config",
			hcType: HCTypeTCP,
			config: HCConfig{
				"port":   3306,
				"send":   "ping",
				"expect": "pong",
			},
		},
		{
			name:   "tcp rejects non-string send",
			hcType: HCTypeTCP,
			config: HCConfig{
				"port": 3306,
				"send": []interface{}{"ping"},
			},
			wantErr: "send must be a string",
		},
		{
			name:   "tls hello rejects invalid port range",
			hcType: HCTypeTLSHello,
			config: HCConfig{
				"port": 65536,
			},
			wantErr: "port must be an integer between 1 and 65535",
		},
		{
			name:   "ping accepts nil config",
			hcType: HCTypePing,
		},
		{
			name:    "unknown type rejected",
			hcType:  HCType("smtp"),
			config:  HCConfig{"port": 25},
			wantErr: "unsupported health check type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHealthCheckConfig(tt.hcType, tt.config)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateHealthCheckConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateHealthCheckConfig() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
