package api

import (
	"context"
	"net/http"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/gin-gonic/gin"
)

// CreateBackendRequest represents the request body for creating a backend
type CreateBackendRequest struct {
	VIPID  string `json:"vip_id" binding:"required"`
	IP     string `json:"ip" binding:"required,ip"`
	Weight int    `json:"weight" binding:"omitempty,min=1,max=100"`
}

// UpdateBackendRequest represents the request body for updating a backend
type UpdateBackendRequest struct {
	IP     string `json:"ip" binding:"omitempty,ip"`
	Weight int    `json:"weight" binding:"omitempty,min=1,max=100"`
}

// createBackend handles POST /api/v1/backends
func (s *Server) createBackend(c *gin.Context) {
	var req CreateBackendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Verify VIP exists
	_, err := s.datastore.GetVIP(ctx, req.VIPID)
	if err != nil {
		s.logger.WithError(err).WithField("vip_id", req.VIPID).Error("VIP not found")
		handleDataStoreError(c, err, "VIP")
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
	if vipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vip_id query parameter is required"})
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

	var req UpdateBackendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	if req.IP != "" {
		backend.IP = req.IP
	}
	if req.Weight != 0 {
		backend.Weight = req.Weight
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
