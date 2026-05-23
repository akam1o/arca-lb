package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// CreateBackendRequest represents the request body for creating a backend
type CreateBackendRequest struct {
	VIPID  string `json:"vip_id" binding:"required"`
	IP     string `json:"ip" binding:"required,ip"`
	Weight int    `json:"weight" binding:"omitempty,min=1,max=100"`
}

// UpdateBackendRequest represents the request body for updating a backend
type UpdateBackendRequest struct {
	IP     *string `json:"ip" binding:"omitempty,ip"`
	Weight *int    `json:"weight" binding:"omitempty,min=1,max=100"`
}

func validateBackendAddressFamily(vip *models.VIP, backendIP string) error {
	if vip == nil {
		return nil
	}

	ip := net.ParseIP(backendIP)
	if ip == nil {
		return nil
	}

	encapType := effectiveEncapType(vip.EncapType)
	isIPv4 := ip.To4() != nil
	switch encapType {
	case models.EncapTypeGRE4, models.EncapTypeL3DSR, models.EncapTypeNAT4:
		if !isIPv4 {
			return badRequestError("backend ip must be IPv4 when encap_type is " + string(encapType))
		}
	case models.EncapTypeGRE6, models.EncapTypeNAT6:
		if isIPv4 {
			return badRequestError("backend ip must be IPv6 when encap_type is " + string(encapType))
		}
	}

	return nil
}

// createBackend handles POST /api/v1/backends
func (s *Server) createBackend(c *gin.Context) {
	var requestFields map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&requestFields, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}
	if err := validateNoDuplicateJSONFields(c); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownJSONFields(requestFields, "vip_id", "ip", "weight"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req CreateBackendRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}
	if err := validateResourceID("vip_id", req.VIPID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Verify VIP exists
	vip, err := s.datastore.GetVIP(ctx, req.VIPID)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", req.VIPID).Error("VIP not found")
		handleDataStoreError(c, err, "VIP")
		return
	}
	if err := validateBackendAddressFamily(vip, req.IP); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create backend model
	backend := &models.Backend{
		VIPID:  req.VIPID,
		IP:     req.IP,
		Weight: req.Weight,
	}

	// Set default weight if not specified
	if backend.Weight == 0 {
		backend.Weight = 1
	}

	// Create backend in datastore
	if err := s.datastore.AddBackend(ctx, backend); err != nil {
		s.logger.WithError(err).Error("Failed to create backend")
		handleDataStoreError(c, err, "Backend")
		return
	}

	s.logger.WithField("backend_id", backend.ID).Info("Backend created successfully")
	c.JSON(http.StatusCreated, backend)
}

// listBackends handles GET /api/v1/backends?vip_id=xxx
func (s *Server) listBackends(c *gin.Context) {
	vipID := c.Query("vip_id")
	if err := validateResourceID("vip_id", vipID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	backends, err := s.datastore.ListBackends(ctx, vipID)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", vipID).Error("Failed to list backends")
		handleDataStoreError(c, err, "Backend")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"backends": backends,
		"count":    len(backends),
	})
}

// getBackend handles GET /api/v1/backends/:id
func (s *Server) getBackend(c *gin.Context) {
	id := c.Param("id")
	if err := validateResourceID("id", id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	backend, err := s.datastore.GetBackend(ctx, id)
	if err != nil {
		s.logger.WithError(err).WithField("backend_id", id).Error("Failed to get backend")
		handleDataStoreError(c, err, "Backend")
		return
	}

	c.JSON(http.StatusOK, backend)
}

// updateBackend handles PUT /api/v1/backends/:id
func (s *Server) updateBackend(c *gin.Context) {
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
	if err := validateNonNullJSONFields(requestFields, "ip", "weight"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateKnownJSONFields(requestFields, "ip", "weight"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req UpdateBackendRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		handleBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Get existing backend
	backend, err := s.datastore.GetBackend(ctx, id)
	if err != nil {
		s.logger.WithError(err).WithField("backend_id", id).Error("Failed to get backend")
		handleDataStoreError(c, err, "Backend")
		return
	}

	// Update fields if provided
	if req.IP != nil {
		backend.IP = *req.IP
	}
	if req.Weight != nil {
		backend.Weight = *req.Weight
	}

	vip, err := s.datastore.GetVIP(ctx, backend.VIPID)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", backend.VIPID).Error("VIP not found for backend")
		handleDataStoreError(c, err, "VIP")
		return
	}
	if err := validateBackendAddressFamily(vip, backend.IP); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update backend in datastore
	if err := s.datastore.UpdateBackend(ctx, backend); err != nil {
		s.logger.WithError(err).WithField("backend_id", id).Error("Failed to update backend")
		handleDataStoreError(c, err, "Backend")
		return
	}

	s.logger.WithField("backend_id", id).Info("Backend updated successfully")
	c.JSON(http.StatusOK, backend)
}

// deleteBackend handles DELETE /api/v1/backends/:id
func (s *Server) deleteBackend(c *gin.Context) {
	id := c.Param("id")
	if err := validateResourceID("id", id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := s.datastore.DeleteBackend(ctx, id); err != nil {
		s.logger.WithError(err).WithField("backend_id", id).Error("Failed to delete backend")
		handleDataStoreError(c, err, "Backend")
		return
	}

	s.logger.WithField("backend_id", id).Info("Backend deleted successfully")
	c.JSON(http.StatusOK, gin.H{"message": "backend deleted successfully"})
}
