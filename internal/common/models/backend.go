package models

import "time"

type Backend struct {
	ID        string    `json:"id"`
	VIPID     string    `json:"vip_id" binding:"required" validate:"required"`
	IP        string    `json:"ip" binding:"required,ip" validate:"required,ip"`
	Weight    int       `json:"weight" binding:"required,min=1,max=100" validate:"required,min=1,max=100"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
