package api

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
)

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

// HealthCheckRequest represents the request body for an optional health check when creating a VIP.
type HealthCheckRequest struct {
	Type      string          `json:"type" binding:"required"`
	Interval  string          `json:"interval" binding:"required"`
	Timeout   string          `json:"timeout" binding:"required"`
	RiseCount int             `json:"rise_count" binding:"omitempty,min=1"`
	FallCount int             `json:"fall_count" binding:"omitempty,min=1"`
	Config    models.HCConfig `json:"config,omitempty"`
}

// CreateVIPRequest represents the request body for creating a VIP
type CreateVIPRequest struct {
	VIP         string              `json:"vip" binding:"required,ip"`
	Port        int                 `json:"port" binding:"required,min=1,max=65535"`
	Protocol    models.Protocol     `json:"protocol" binding:"required,oneof=TCP UDP"`
	LBMethod    models.LBMethod     `json:"lb_method" binding:"omitempty,oneof=maglev"`
	EncapType   models.EncapType    `json:"encap_type" binding:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6"`
	DSCP        *uint8              `json:"dscp" binding:"omitempty,min=0,max=63"`
	HealthCheck *HealthCheckRequest `json:"health_check,omitempty"`
}

// UpdateVIPRequest represents the request body for updating a VIP
type UpdateVIPRequest struct {
	VIP       string           `json:"vip" binding:"omitempty,ip"`
	Port      int              `json:"port" binding:"omitempty,min=1,max=65535"`
	Protocol  models.Protocol  `json:"protocol" binding:"omitempty,oneof=TCP UDP"`
	LBMethod  models.LBMethod  `json:"lb_method" binding:"omitempty,oneof=maglev"`
	EncapType models.EncapType `json:"encap_type" binding:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6"`
	DSCP      *uint8           `json:"dscp" binding:"omitempty,min=0,max=63"`
}

func parseOptionalInt(v any) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func validateHealthCheckRequest(req *HealthCheckRequest) error {
	if req == nil {
		return nil
	}

	hcType := models.HCType(strings.ToLower(req.Type))
	switch hcType {
	case models.HCTypeHTTP, models.HCTypeHTTPS, models.HCTypeTCP, models.HCTypePing:
	default:
		return badRequestError("invalid health check type")
	}

	switch hcType {
	case models.HCTypeHTTP, models.HCTypeHTTPS, models.HCTypeTCP:
		if req.Config == nil {
			return badRequestError("health_check.config is required for this type")
		}
		portRaw, ok := req.Config["port"]
		if !ok {
			return badRequestError("health_check.config.port is required")
		}
		port, ok := parseOptionalInt(portRaw)
		if !ok || port < 1 || port > 65535 {
			return badRequestError("health_check.config.port must be an integer between 1 and 65535")
		}

		if hcType == models.HCTypeHTTP || hcType == models.HCTypeHTTPS {
			if path, ok := req.Config["path"]; ok && path != nil {
				if _, ok := path.(string); !ok {
					return badRequestError("health_check.config.path must be a string")
				}
			}
			if expectedCodes, ok := req.Config["expected_codes"]; ok && expectedCodes != nil {
				arr, ok := expectedCodes.([]interface{})
				if !ok {
					return badRequestError("health_check.config.expected_codes must be an array of integers")
				}
				for _, code := range arr {
					if _, ok := parseOptionalInt(code); !ok {
						return badRequestError("health_check.config.expected_codes must be an array of integers")
					}
				}
			}
			if headers, ok := req.Config["headers"]; ok && headers != nil {
				hm, ok := headers.(map[string]interface{})
				if !ok {
					return badRequestError("health_check.config.headers must be an object")
				}
				for _, v := range hm {
					if v == nil {
						continue
					}
					if _, ok := v.(string); !ok {
						return badRequestError("health_check.config.headers values must be strings")
					}
				}
			}
			if tlsSkipVerify, ok := req.Config["tls_skip_verify"]; ok && tlsSkipVerify != nil {
				if _, ok := tlsSkipVerify.(bool); !ok {
					return badRequestError("health_check.config.tls_skip_verify must be a boolean")
				}
			}
		}
	case models.HCTypePing:
		// No config required.
	}

	return nil
}

func parseHealthCheckDuration(value, field string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, badRequestError("invalid health check " + field)
	}
	if duration%time.Second != 0 {
		return 0, badRequestError("health check " + field + " must be a whole number of seconds")
	}
	return duration, nil
}

// createVIP handles POST /api/v1/vips
func (s *Server) createVIP(c *gin.Context) {
	var req CreateVIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateHealthCheckRequest(req.HealthCheck); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.EncapType == models.EncapTypeL3DSR && req.DSCP != nil && *req.DSCP == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dscp must be 1-63 when encap_type is L3DSR (DSCP mode)"})
		return
	}

	// Create VIP model
	vip := &models.VIP{
		VIP:       req.VIP,
		Port:      req.Port,
		Protocol:  req.Protocol,
		LBMethod:  req.LBMethod,
		EncapType: req.EncapType,
		DSCP:      req.DSCP,
	}

	// Set default LB method if not specified
	if vip.LBMethod == "" {
		vip.LBMethod = models.LBMethodMaglev
	}

	// Set health check if provided
	if req.HealthCheck != nil {
		hcType := models.HCType(strings.ToLower(req.HealthCheck.Type))

		interval, err := parseHealthCheckDuration(req.HealthCheck.Interval, "interval")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		timeout, err := parseHealthCheckDuration(req.HealthCheck.Timeout, "timeout")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if timeout >= interval {
			c.JSON(http.StatusBadRequest, gin.H{"error": "health check timeout must be less than interval"})
			return
		}

		riseCount := req.HealthCheck.RiseCount
		if riseCount == 0 {
			riseCount = 3
		}
		fallCount := req.HealthCheck.FallCount
		if fallCount == 0 {
			fallCount = 3
		}

		vip.HealthCheck = &models.HealthCheck{
			Type:        hcType,
			IntervalSec: int(interval / time.Second),
			TimeoutSec:  int(timeout / time.Second),
			RiseCount:   riseCount,
			FallCount:   fallCount,
			Config:      req.HealthCheck.Config,
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
	if req.EncapType != "" {
		vip.EncapType = req.EncapType
	}
	if req.DSCP != nil {
		vip.DSCP = req.DSCP
	}

	if vip.EncapType == models.EncapTypeL3DSR && vip.DSCP != nil && *vip.DSCP == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dscp must be 1-63 when encap_type is L3DSR (DSCP mode)"})
		return
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
