package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
)

// HealthCheckRequest represents the request body for an optional health check when creating a VIP.
type HealthCheckRequest struct {
	Type     string `json:"type" binding:"required"`
	Interval string `json:"interval" binding:"required"`
	Timeout  string `json:"timeout" binding:"required"`
}

// CreateVIPRequest represents the request body for creating a VIP
type CreateVIPRequest struct {
	VIP         string              `json:"vip" binding:"required,ip"`
	Port        int                 `json:"port" binding:"required,min=1,max=65535"`
	Protocol    models.Protocol     `json:"protocol" binding:"required,oneof=TCP UDP"`
	LBMethod    models.LBMethod     `json:"lb_method" binding:"omitempty,oneof=maglev"`
	HealthCheck *HealthCheckRequest `json:"health_check,omitempty"`
}

// UpdateVIPRequest represents the request body for updating a VIP
type UpdateVIPRequest struct {
	VIP      string          `json:"vip" binding:"omitempty,ip"`
	Port     int             `json:"port" binding:"omitempty,min=1,max=65535"`
	Protocol models.Protocol `json:"protocol" binding:"omitempty,oneof=TCP UDP"`
	LBMethod models.LBMethod `json:"lb_method" binding:"omitempty,oneof=maglev"`
}

// createVIP handles POST /api/v1/vips
func (s *Server) createVIP(c *gin.Context) {
	var req CreateVIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create VIP model
	vip := &models.VIP{
		VIP:      req.VIP,
		Port:     req.Port,
		Protocol: req.Protocol,
		LBMethod: req.LBMethod,
	}

	// Set default LB method if not specified
	if vip.LBMethod == "" {
		vip.LBMethod = models.LBMethodMaglev
	}

	// Set health check if provided
	if req.HealthCheck != nil {
		hcType := models.HCType(strings.ToLower(req.HealthCheck.Type))
		switch hcType {
		case models.HCTypeHTTP, models.HCTypeHTTPS, models.HCTypeTCP, models.HCTypePing:
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid health check type"})
			return
		}

		interval, err := time.ParseDuration(req.HealthCheck.Interval)
		if err != nil || interval <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid health check interval"})
			return
		}

		timeout, err := time.ParseDuration(req.HealthCheck.Timeout)
		if err != nil || timeout <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid health check timeout"})
			return
		}

		vip.HealthCheck = &models.HealthCheck{
			Type:        hcType,
			IntervalSec: int(interval.Seconds()),
			TimeoutSec:  int(timeout.Seconds()),
			RiseCount:   3,
			FallCount:   3,
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Create VIP in datastore
	if err := s.datastore.CreateVIP(ctx, vip); err != nil {
		s.logger.WithError(err).Error("Failed to create VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	if vip.HealthCheck != nil && vip.HealthCheck.VIPID == "" {
		vip.HealthCheck.VIPID = vip.ID
	}

	s.logger.WithField("vip_id", vip.ID).Info("VIP created successfully")
	c.JSON(http.StatusCreated, vip)
}

// listVIPs handles GET /api/v1/vips
func (s *Server) listVIPs(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	vips, err := s.datastore.ListVIPs(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to list VIPs")
		handleDataStoreError(c, err, "VIP")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"vips":  vips,
		"count": len(vips),
	})
}

// getVIP handles GET /api/v1/vips/:id
func (s *Server) getVIP(c *gin.Context) {
	id := c.Param("id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	vip, err := s.datastore.GetVIP(ctx, id)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to get VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	c.JSON(http.StatusOK, vip)
}

// updateVIP handles PUT /api/v1/vips/:id
func (s *Server) updateVIP(c *gin.Context) {
	id := c.Param("id")

	var req UpdateVIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Get existing VIP
	vip, err := s.datastore.GetVIP(ctx, id)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to get VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	// Update fields if provided
	if req.VIP != "" {
		vip.VIP = req.VIP
	}
	if req.Port != 0 {
		vip.Port = req.Port
	}
	if req.Protocol != "" {
		vip.Protocol = req.Protocol
	}
	if req.LBMethod != "" {
		vip.LBMethod = req.LBMethod
	}

	// Update VIP in datastore
	if err := s.datastore.UpdateVIP(ctx, vip); err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to update VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	s.logger.WithField("vip_id", id).Info("VIP updated successfully")
	c.JSON(http.StatusOK, vip)
}

// deleteVIP handles DELETE /api/v1/vips/:id
func (s *Server) deleteVIP(c *gin.Context) {
	id := c.Param("id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := s.datastore.DeleteVIP(ctx, id); err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to delete VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	s.logger.WithField("vip_id", id).Info("VIP deleted successfully")
	c.JSON(http.StatusOK, gin.H{"message": "VIP deleted successfully"})
}
