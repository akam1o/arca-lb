package models

import "time"

type HCType string

const (
	HCTypeHTTP  HCType = "http"
	HCTypeHTTPS HCType = "https"
	HCTypeTCP   HCType = "tcp"
	HCTypePing  HCType = "ping"
)

// HCConfig holds additional health check settings.
type HCConfig map[string]interface{}

type HealthCheck struct {
	ID          string    `json:"id"`
	VIPID       string    `json:"vip_id" binding:"required" validate:"required"`
	Type        HCType    `json:"type" binding:"required,oneof=http https tcp ping" validate:"required,oneof=http https tcp ping"`
	IntervalSec int       `json:"interval_sec" binding:"required,min=1" validate:"required,min=1"`
	TimeoutSec  int       `json:"timeout_sec" binding:"required,min=1" validate:"required,min=1"`
	RiseCount   int       `json:"rise_count" binding:"required,min=1" validate:"required,min=1"`
	FallCount   int       `json:"fall_count" binding:"required,min=1" validate:"required,min=1"`
	Config      HCConfig  `json:"config,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
