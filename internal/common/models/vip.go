package models

import "time"

type Protocol string
type LBMethod string

const (
	ProtocolTCP Protocol = "TCP"
	ProtocolUDP Protocol = "UDP"
)

const (
	LBMethodMaglev LBMethod = "maglev"
)

type VIP struct {
	ID        string    `json:"id"` // UUID or auto-generated ID
	VIP       string    `json:"vip" binding:"required,ip" validate:"required,ip"`
	Port      int       `json:"port" binding:"required,min=1,max=65535" validate:"required,min=1,max=65535"`
	Protocol  Protocol  `json:"protocol" binding:"required,oneof=TCP UDP" validate:"required,oneof=TCP UDP"`
	LBMethod  LBMethod  `json:"lb_method" binding:"omitempty,oneof=maglev" validate:"omitempty,oneof=maglev"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Related optional data.
	HealthCheck *HealthCheck `json:"health_check,omitempty"`
	Backends    []Backend    `json:"backends,omitempty"`
}
