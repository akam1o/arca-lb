package models

import "time"

type SystemMetadata struct {
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}
