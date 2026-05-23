package api

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

// HealthCheckRequest represents the request body for an optional health check when creating a VIP.
type HealthCheckRequest struct {
	Type            string          `json:"type" binding:"required"`
	Interval        string          `json:"interval" binding:"omitempty"`
	Timeout         string          `json:"timeout" binding:"omitempty"`
	IntervalSeconds *int            `json:"interval_seconds" binding:"omitempty,min=1,max=2147483647"`
	TimeoutSeconds  *int            `json:"timeout_seconds" binding:"omitempty,min=1,max=2147483647"`
	RiseCount       int             `json:"rise_count" binding:"omitempty,min=1,max=2147483647"`
	FallCount       int             `json:"fall_count" binding:"omitempty,min=1,max=2147483647"`
	Config          models.HCConfig `json:"config,omitempty"`
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
	VIP         *string             `json:"vip" binding:"omitempty,ip"`
	Port        *int                `json:"port" binding:"omitempty,min=1,max=65535"`
	Protocol    *models.Protocol    `json:"protocol" binding:"omitempty,oneof=TCP UDP"`
	LBMethod    *models.LBMethod    `json:"lb_method" binding:"omitempty,oneof=maglev"`
	EncapType   *models.EncapType   `json:"encap_type" binding:"omitempty,oneof=GRE4 GRE6 L3DSR NAT4 NAT6"`
	DSCP        *uint8              `json:"dscp" binding:"omitempty,min=0,max=63"`
	HealthCheck *HealthCheckRequest `json:"health_check,omitempty"`
}

func validateResourceID(field, value string) error {
	if err := datastore.ValidateResourceID(field, value); err != nil {
		return badRequestError(err.Error())
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

func healthCheckSecondsFromRequest(durationValue string, secondsValue *int, durationField, secondsField string) (int, error) {
	if durationValue != "" && secondsValue != nil {
		return 0, badRequestError("health_check." + durationField + " and health_check." + secondsField + " must not both be set")
	}

	if secondsValue != nil {
		if *secondsValue <= 0 {
			return 0, badRequestError("health check " + secondsField + " must be positive")
		}
		if *secondsValue > models.MaxHealthCheckSeconds {
			return 0, badRequestError("health check " + secondsField + " must be at most 2147483647 seconds")
		}
		return *secondsValue, nil
	}

	if durationValue == "" {
		return 0, badRequestError("health_check." + secondsField + " or health_check." + durationField + " is required")
	}

	duration, err := parseHealthCheckDuration(durationValue, durationField)
	if err != nil {
		return 0, err
	}
	return int(duration / time.Second), nil
}

func healthCheckFromRequest(req *HealthCheckRequest) (*models.HealthCheck, error) {
	if req == nil {
		return nil, nil
	}

	hcType := models.HCType(strings.ToLower(req.Type))

	intervalSec, err := healthCheckSecondsFromRequest(req.Interval, req.IntervalSeconds, "interval", "interval_seconds")
	if err != nil {
		return nil, err
	}

	timeoutSec, err := healthCheckSecondsFromRequest(req.Timeout, req.TimeoutSeconds, "timeout", "timeout_seconds")
	if err != nil {
		return nil, err
	}
	if timeoutSec >= intervalSec {
		return nil, badRequestError("health check timeout must be less than interval")
	}

	riseCount := req.RiseCount
	if riseCount == 0 {
		riseCount = 3
	}
	fallCount := req.FallCount
	if fallCount == 0 {
		fallCount = 2
	}

	return &models.HealthCheck{
		Type:        hcType,
		IntervalSec: intervalSec,
		TimeoutSec:  timeoutSec,
		RiseCount:   riseCount,
		FallCount:   fallCount,
		Config:      req.Config,
	}, nil
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

func validateNoDuplicateJSONFields(c *gin.Context) error {
	raw, ok := c.Get(gin.BodyBytesKey)
	if !ok {
		return nil
	}
	body, ok := raw.([]byte)
	if !ok {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	return validateNoDuplicateJSONValue(decoder, "")
}

func validateNoDuplicateJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return badRequestError("invalid JSON object key")
			}

			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			if _, exists := seen[key]; exists {
				return badRequestError("duplicate field " + strconv.Quote(fieldPath))
			}
			seen[key] = struct{}{}

			if err := validateNoDuplicateJSONValue(decoder, fieldPath); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := validateNoDuplicateJSONValue(decoder, path); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return nil
	}
}

func validateKnownJSONFields(fields map[string]json.RawMessage, names ...string) error {
	return validateKnownJSONFieldsWithPrefix(fields, "", names...)
}

func validateKnownNestedJSONFields(fields map[string]json.RawMessage, field string, names ...string) error {
	raw, ok := fields[field]
	if !ok || rawJSONIsNull(raw) {
		return nil
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return badRequestError(field + " must be a JSON object")
	}

	return validateKnownJSONFieldsWithPrefix(nested, field, names...)
}

func validateKnownJSONFieldsWithPrefix(fields map[string]json.RawMessage, prefix string, names ...string) error {
	if fields == nil {
		return badRequestError("request body must be a JSON object")
	}

	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}

	for name := range fields {
		if _, ok := allowed[name]; ok {
			continue
		}
		fieldName := name
		if prefix != "" {
			fieldName = prefix + "." + name
		}
		return badRequestError("unknown field " + strconv.Quote(fieldName))
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
	var requestFields map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&requestFields, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}
	if err := validateNoDuplicateJSONFields(c); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownJSONFields(requestFields, "vip", "port", "protocol", "lb_method", "encap_type", "dscp", "health_check"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownNestedJSONFields(requestFields, "health_check", "type", "interval", "timeout", "interval_seconds", "timeout_seconds", "rise_count", "fall_count", "config"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req CreateVIPRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
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
		healthCheck, err := healthCheckFromRequest(req.HealthCheck)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		vip.HealthCheck = healthCheck
	}

	ctx, cancel := s.datastoreContext(c)
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
	ctx, cancel := s.datastoreContext(c)
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

	ctx, cancel := s.datastoreContext(c)
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
	if err := validateNoDuplicateJSONFields(c); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateNonNullJSONFields(requestFields, "vip", "port", "protocol", "lb_method", "encap_type"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownJSONFields(requestFields, "vip", "port", "protocol", "lb_method", "encap_type", "dscp", "health_check"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownNestedJSONFields(requestFields, "health_check", "type", "interval", "timeout", "interval_seconds", "timeout_seconds", "rise_count", "fall_count", "config"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req UpdateVIPRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}

	ctx, cancel := s.datastoreContext(c)
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
	if rawHealthCheck, ok := requestFields["health_check"]; ok {
		if rawJSONIsNull(rawHealthCheck) {
			vip.HealthCheck = nil
		} else {
			if err := validateHealthCheckRequest(req.HealthCheck); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			healthCheck, err := healthCheckFromRequest(req.HealthCheck)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			vip.HealthCheck = healthCheck
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

	ctx, cancel := s.datastoreContext(c)
	defer cancel()

	if err := s.datastore.DeleteVIP(ctx, id); err != nil {
		s.logger.WithError(err).WithField("vip_id", id).Error("Failed to delete VIP")
		handleDataStoreError(c, err, "VIP")
		return
	}

	s.logger.WithField("vip_id", id).Info("VIP deleted successfully")
	c.Status(http.StatusNoContent)
}
