package models

import "time"

type Protocol string
type LBMethod string
type EncapType string

const (
	ProtocolTCP Protocol = "TCP"
	ProtocolUDP Protocol = "UDP"
)

const (
	LBMethodMaglev LBMethod = "maglev"
)

const (
	EncapTypeGRE4  EncapType = "GRE4"
	EncapTypeGRE6  EncapType = "GRE6"
	EncapTypeL3DSR EncapType = "L3DSR"
	EncapTypeNAT4  EncapType = "NAT4"
	EncapTypeNAT6  EncapType = "NAT6"
)

type VIP struct {
	ID        string    `json:"id"` // UUID or auto-generated ID
	VIP       string    `json:"vip" binding:"required,ip" validate:"required,ip"`
	Port      int       `json:"port" binding:"required,min=1,max=65535" validate:"required,min=1,max=65535"`
	Protocol  Protocol  `json:"protocol" binding:"required,oneof=TCP UDP" validate:"required,oneof=TCP UDP"`
	LBMethod  LBMethod  `json:"lb_method" binding:"omitempty,oneof=maglev" validate:"omitempty,oneof=maglev"`
	EncapType EncapType `json:"encap_type,omitempty" binding:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6" validate:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6"`
	DSCP      *uint8    `json:"dscp,omitempty" binding:"omitempty,min=0,max=63" validate:"omitempty,min=0,max=63"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Related optional data.
	HealthCheck *HealthCheck `json:"health_check,omitempty"`
	Backends    []Backend    `json:"backends,omitempty"`
}
