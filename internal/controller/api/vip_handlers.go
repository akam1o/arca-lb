package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

const maxResourceIDBytes = 256

// HealthCheckRequest represents the request body for an optional health check when creating a VIP.
type HealthCheckRequest struct {
	Type      string          `json:"type" binding:"required"`
	Interval  string          `json:"interval" binding:"required"`
	Timeout   string          `json:"timeout" binding:"required"`
	RiseCount int             `json:"rise_count" binding:"omitempty,min=1,max=2147483647"`
	FallCount int             `json:"fall_count" binding:"omitempty,min=1,max=2147483647"`
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
	VIP       *string           `json:"vip" binding:"omitempty,ip"`
	Port      *int              `json:"port" binding:"omitempty,min=1,max=65535"`
	Protocol  *models.Protocol  `json:"protocol" binding:"omitempty,oneof=TCP UDP"`
	LBMethod  *models.LBMethod  `json:"lb_method" binding:"omitempty,oneof=maglev"`
	EncapType *models.EncapType `json:"encap_type" binding:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6"`
	DSCP      *uint8            `json:"dscp" binding:"omitempty,min=0,max=63"`
}

func validateResourceID(field, value string) error {
	if value == "" {
		return badRequestError(field + " is required")
	}
	if len(value) > maxResourceIDBytes {
		return badRequestError(field + " must be at most 256 bytes")
	}
	if strings.Contains(value, "/") {
		return badRequestError(field + " must not contain '/'")
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return badRequestError(field + " must not contain whitespace")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return badRequestError(field + " must not contain control characters")
	}
	return nil
}

func validateHealthCheckRequest(req *HealthCheckRequest) error {
	if req == nil {
		return nil
	}

	hcType := models.HCType(strings.ToLower(req.Type))
	switch hcType {
	case models.HCTypeHTTP, models.HCTypeHTTPS, models.HCTypeTCP, models.HCTypePing, models.HCTypeTLSHello:
	default:
		return badRequestError("invalid health check type")
	}

	if err := models.ValidateHealthCheckConfig(hcType, req.Config); err != nil {
		return healthCheckConfigBadRequest(err)
	}

	return nil
}

func healthCheckConfigBadRequest(err error) error {
	msg := err.Error()
	if strings.HasPrefix(msg, "config is required") {
		return badRequestError("health_check.config is required for this type")
	}
	if strings.HasPrefix(msg, "unsupported health check type") {
		return badRequestError("invalid health check type")
	}
	return badRequestError("health_check.config." + msg)
}

func parseHealthCheckDuration(value, field string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, badRequestError("invalid health check " + field)
	}
	if duration%time.Second != 0 {
		return 0, badRequestError("health check " + field + " must be a whole number of seconds")
	}
	if duration/time.Second > time.Duration(models.MaxHealthCheckSeconds) {
		return 0, badRequestError("health check " + field + " must be at most 2147483647 seconds")
	}
	return duration, nil
}

func effectiveEncapType(encapType models.EncapType) models.EncapType {
	if encapType == "" {
		return models.EncapTypeL3DSR
	}
	return encapType
}

func validateDSCPForEncap(encapType models.EncapType, dscp *uint8) error {
	if effectiveEncapType(encapType) == models.EncapTypeL3DSR && dscp != nil && *dscp == 0 {
		return badRequestError("dscp must be 1-63 when encap_type is L3DSR (DSCP mode)")
	}
	return nil
}

func rawJSONIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func validateNonNullJSONFields(fields map[string]json.RawMessage, names ...string) error {
	if fields == nil {
		return badRequestError("request body must be a JSON object")
	}
	for _, name := range names {
		if raw, ok := fields[name]; ok && rawJSONIsNull(raw) {
			return badRequestError(name + " must not be null")
		}
	}
	return nil
}

func validateEncapAddressFamily(vip string, encapType models.EncapType) error {
	ip := net.ParseIP(vip)
	if ip == nil {
		return nil
	}

	effectiveEncap := effectiveEncapType(encapType)
	isIPv4 := ip.To4() != nil
	switch effectiveEncap {
	case models.EncapTypeL3DSR, models.EncapTypeNAT4:
		if !isIPv4 {
			return badRequestError("encap_type " + string(effectiveEncap) + " requires an IPv4 vip")
		}
	case models.EncapTypeNAT6:
		if isIPv4 {
			return badRequestError("encap_type NAT6 requires an IPv6 vip")
		}
	}

	return nil
}

func validateExistingBackendAddressFamilies(backends []models.Backend, vip *models.VIP) error {
	for _, backend := range backends {
		if err := validateBackendAddressFamily(vip, backend.IP); err != nil {
			return badRequestError("existing backend " + backend.ID + ": " + err.Error())
		}
	}

	return nil
}

// createVIP handles POST /api/v1/vips
func (s *Server) createVIP(c *gin.Context) {
	var req CreateVIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleBindError(c, err)
		return
	}
	if err := validateHealthCheckRequest(req.HealthCheck); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateEncapAddressFamily(req.VIP, req.EncapType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateDSCPForEncap(req.EncapType, req.DSCP); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			fallCount = 2
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
	if err := validateResourceID("id", id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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
	if err := validateResourceID("id", id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var requestFields map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&requestFields, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}
	if err := validateNonNullJSONFields(requestFields, "vip", "port", "protocol", "lb_method", "encap_type"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req UpdateVIPRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		handleBindError(c, err)
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
	if req.VIP != nil {
		vip.VIP = *req.VIP
	}
	if req.Port != nil {
		vip.Port = *req.Port
	}
	if req.Protocol != nil {
		vip.Protocol = *req.Protocol
	}
	if req.LBMethod != nil {
		vip.LBMethod = *req.LBMethod
	}
	if req.EncapType != nil {
		vip.EncapType = *req.EncapType
	}
	if rawDSCP, ok := requestFields["dscp"]; ok {
		if rawJSONIsNull(rawDSCP) {
			vip.DSCP = nil
		} else {
			vip.DSCP = req.DSCP
		}
	}

	if err := validateEncapAddressFamily(vip.VIP, vip.EncapType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateDSCPForEncap(vip.EncapType, vip.DSCP); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	backends, err := s.datastore.ListBackends(ctx, id)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to list backends for VIP update validation")
		handleDataStoreError(c, err, "Backend")
		return
	}
	if err := validateExistingBackendAddressFamilies(backends, vip); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	if err := validateResourceID("id", id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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
