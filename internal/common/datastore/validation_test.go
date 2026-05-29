package datastore

import (
	"errors"
	"strings"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/models"
)

func TestValidateResourceID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{
			name:  "simple id",
			value: "vip-1",
		},
		{
			name:  "max length",
			value: strings.Repeat("a", MaxResourceIDBytes),
		},
		{
			name:    "empty",
			value:   "",
			wantErr: true,
		},
		{
			name:    "contains slash",
			value:   "vip/1",
			wantErr: true,
		},
		{
			name:    "contains whitespace",
			value:   "vip 1",
			wantErr: true,
		},
		{
			name:    "contains control character",
			value:   "vip\x001",
			wantErr: true,
		},
		{
			name:    "too long",
			value:   strings.Repeat("a", MaxResourceIDBytes+1),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceID("id", tt.value)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestValidateVIPForWriteRejectsEncapAddressFamilyMismatch(t *testing.T) {
	tests := []struct {
		name      string
		vip       string
		encapType models.EncapType
	}{
		{
			name:      "l3dsr requires ipv4 vip",
			vip:       "2001:db8::10",
			encapType: models.EncapTypeL3DSR,
		},
		{
			name:      "nat4 requires ipv4 vip",
			vip:       "2001:db8::10",
			encapType: models.EncapTypeNAT4,
		},
		{
			name:      "nat6 requires ipv6 vip",
			vip:       "192.0.2.10",
			encapType: models.EncapTypeNAT6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVIPForWrite(&models.VIP{
				VIP:       tt.vip,
				Port:      80,
				Protocol:  models.ProtocolTCP,
				LBMethod:  models.LBMethodMaglev,
				EncapType: tt.encapType,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
			}
		})
	}
}

func TestValidateVIPForWriteRejectsZeroDSCPForL3DSR(t *testing.T) {
	zero := uint8(0)
	tests := []struct {
		name      string
		encapType models.EncapType
	}{
		{
			name:      "explicit l3dsr",
			encapType: models.EncapTypeL3DSR,
		},
		{
			name: "default l3dsr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVIPForWrite(&models.VIP{
				VIP:       "192.0.2.10",
				Port:      80,
				Protocol:  models.ProtocolTCP,
				LBMethod:  models.LBMethodMaglev,
				EncapType: tt.encapType,
				DSCP:      &zero,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
			}
		})
	}
}

func TestValidateBackendAddressFamilyForVIP(t *testing.T) {
	tests := []struct {
		name      string
		encapType models.EncapType
		backendIP string
		wantErr   bool
	}{
		{
			name:      "l3dsr accepts ipv4 backend",
			encapType: models.EncapTypeL3DSR,
			backendIP: "10.0.0.1",
		},
		{
			name:      "l3dsr rejects ipv6 backend",
			encapType: models.EncapTypeL3DSR,
			backendIP: "2001:db8::20",
			wantErr:   true,
		},
		{
			name:      "gre6 rejects ipv4 backend",
			encapType: models.EncapTypeGRE6,
			backendIP: "10.0.0.1",
			wantErr:   true,
		},
		{
			name:      "nat6 accepts ipv6 backend",
			encapType: models.EncapTypeNAT6,
			backendIP: "2001:db8::20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBackendAddressFamilyForVIP(
				&models.VIP{EncapType: tt.encapType},
				&models.Backend{IP: tt.backendIP},
			)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
