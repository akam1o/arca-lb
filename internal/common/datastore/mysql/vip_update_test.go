package mysql

import (
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
)

func TestVIPUpdateValuesClearsNullableFields(t *testing.T) {
	dscp := uint8(10)
	encapType := "L3DSR"
	now := time.Now()
	fallback := VIPRecord{
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  "TCP",
		LBMethod:  "maglev",
		EncapType: &encapType,
		DSCP:      &dscp,
		CreatedAt: now,
		UpdatedAt: now,
	}
	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		LBMethod:  models.LBMethodMaglev,
		CreatedAt: now,
		UpdatedAt: now,
	}

	updates := vipUpdateValues(vip, fallback)

	if got := updates["encap_type"]; got != nil {
		t.Fatalf("encap_type update = %#v, want nil", got)
	}
	if got := updates["dscp"]; got != nil {
		t.Fatalf("dscp update = %#v, want nil", got)
	}
}

func TestVIPUpdateValuesUsesNullableValuesWhenPresent(t *testing.T) {
	dscp := uint8(20)
	now := time.Now()
	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		LBMethod:  models.LBMethodMaglev,
		EncapType: models.EncapTypeGRE4,
		DSCP:      &dscp,
		CreatedAt: now,
		UpdatedAt: now,
	}

	updates := vipUpdateValues(vip, VIPRecord{})

	if got := updates["encap_type"]; got != "GRE4" {
		t.Fatalf("encap_type update = %#v, want GRE4", got)
	}
	if got := updates["dscp"]; got != uint8(20) {
		t.Fatalf("dscp update = %#v, want 20", got)
	}
}
