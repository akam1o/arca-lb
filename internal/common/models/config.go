package models

// Config aggregates VIP, HealthCheck, and Backends for agent delivery.
type Config struct {
	Revision int64       `json:"revision"`
	VIPs     []VIPConfig `json:"vips"`
}

// VIPConfig is the VIP configuration delivered to agents.
type VIPConfig struct {
	VIP         VIP          `json:"vip"`
	HealthCheck *HealthCheck `json:"health_check,omitempty"`
	Backends    []Backend    `json:"backends"`
}
