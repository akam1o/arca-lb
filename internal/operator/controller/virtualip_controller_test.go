package controller

import (
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

func TestValidateSpecL3DSRAllowsMissingDSCP(t *testing.T) {
	spec := v1alpha1.VirtualIPSpec{
		Address:   "203.0.113.10",
		Port:      80,
		Protocol:  v1alpha1.ProtocolTCP,
		EncapType: v1alpha1.EncapTypeL3DSR,
		Backends: []v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 100},
		},
	}

	if err := validateSpec(&spec); err != nil {
		t.Fatalf("validateSpec rejected L3DSR without per-VIP DSCP: %v", err)
	}
}

func TestValidateSpecL3DSRRejectsInvalidDSCPOverride(t *testing.T) {
	dscp := uint8(0)
	spec := v1alpha1.VirtualIPSpec{
		Address:   "203.0.113.10",
		Port:      80,
		Protocol:  v1alpha1.ProtocolTCP,
		EncapType: v1alpha1.EncapTypeL3DSR,
		DSCP:      &dscp,
		Backends: []v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: 100},
		},
	}

	if err := validateSpec(&spec); err == nil {
		t.Fatal("expected invalid DSCP override to be rejected")
	}
}
