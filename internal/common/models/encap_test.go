package models

import "testing"

func TestEffectiveEncapTypeDefaultsToL3DSR(t *testing.T) {
	if got := EffectiveEncapType(""); got != EncapTypeL3DSR {
		t.Fatalf("EffectiveEncapType(\"\") = %q, want %q", got, EncapTypeL3DSR)
	}
	if got := EffectiveEncapType(EncapTypeNAT6); got != EncapTypeNAT6 {
		t.Fatalf("EffectiveEncapType(NAT6) = %q, want %q", got, EncapTypeNAT6)
	}
}

func TestEncapAddressFamilyRequirements(t *testing.T) {
	tests := []struct {
		name              string
		encapType         EncapType
		wantIPv4VIP       bool
		wantIPv6VIP       bool
		wantIPv4Backend   bool
		wantIPv6Backend   bool
		wantValidNonEmpty bool
	}{
		{
			name:              "empty",
			encapType:         "",
			wantValidNonEmpty: false,
		},
		{
			name:              "GRE4",
			encapType:         EncapTypeGRE4,
			wantIPv4Backend:   true,
			wantValidNonEmpty: true,
		},
		{
			name:              "GRE6",
			encapType:         EncapTypeGRE6,
			wantIPv6Backend:   true,
			wantValidNonEmpty: true,
		},
		{
			name:              "L3DSR",
			encapType:         EncapTypeL3DSR,
			wantIPv4VIP:       true,
			wantIPv4Backend:   true,
			wantValidNonEmpty: true,
		},
		{
			name:              "NAT4",
			encapType:         EncapTypeNAT4,
			wantIPv4VIP:       true,
			wantIPv4Backend:   true,
			wantValidNonEmpty: true,
		},
		{
			name:              "NAT6",
			encapType:         EncapTypeNAT6,
			wantIPv6VIP:       true,
			wantIPv6Backend:   true,
			wantValidNonEmpty: true,
		},
		{
			name:              "invalid",
			encapType:         EncapType("VXLAN"),
			wantValidNonEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidEncapType(tt.encapType); got != tt.wantValidNonEmpty {
				t.Fatalf("IsValidEncapType() = %v, want %v", got, tt.wantValidNonEmpty)
			}
			if got := EncapRequiresIPv4VIP(tt.encapType); got != tt.wantIPv4VIP {
				t.Fatalf("EncapRequiresIPv4VIP() = %v, want %v", got, tt.wantIPv4VIP)
			}
			if got := EncapRequiresIPv6VIP(tt.encapType); got != tt.wantIPv6VIP {
				t.Fatalf("EncapRequiresIPv6VIP() = %v, want %v", got, tt.wantIPv6VIP)
			}
			if got := EncapRequiresIPv4Backend(tt.encapType); got != tt.wantIPv4Backend {
				t.Fatalf("EncapRequiresIPv4Backend() = %v, want %v", got, tt.wantIPv4Backend)
			}
			if got := EncapRequiresIPv6Backend(tt.encapType); got != tt.wantIPv6Backend {
				t.Fatalf("EncapRequiresIPv6Backend() = %v, want %v", got, tt.wantIPv6Backend)
			}
		})
	}
}
