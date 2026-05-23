package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Server represents the REST API server
type Server struct {
	config     *config.Config
	router     *gin.Engine
	httpServer *http.Server
	datastore  datastore.DataStore
	logger     *logrus.Logger
}

// NewServer creates a new REST API server instance
func NewServer(cfg *config.Config, ds datastore.DataStore, logger *logrus.Logger) *Server {
	// Set Gin mode based on log level
	if cfg.Log.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		logger.WithError(err).Warn("Failed to disable trusted proxies")
	}

	server := &Server{
		config:    cfg,
		router:    router,
		datastore: ds,
		logger:    logger,
	}

	// Setup middleware
	server.setupMiddleware()

	// Setup routes
	server.setupRoutes()

	return server
}

// setupMiddleware configures middleware for the server
func (s *Server) setupMiddleware() {
	// Recovery middleware
	s.router.Use(gin.Recovery())

	// Request body size limit
	s.router.Use(s.bodyLimitMiddleware())

	// Logging middleware
	s.router.Use(s.loggingMiddleware())

	// CORS middleware
	s.router.Use(s.corsMiddleware())
}

func (s *Server) bodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := s.config.Server.MaxBodyBytes
		if limit <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

func handleBindError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

// loggingMiddleware returns a Gin middleware for request logging
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		s.logger.WithFields(logrus.Fields{
			"status":     statusCode,
			"method":     method,
			"path":       path,
			"ip":         clientIP,
			"latency_ms": latency.Milliseconds(),
		}).Info("HTTP request")
	}
}

// containsVary checks if a Vary header value contains a specific field
func containsVary(varyHeader, field string) bool {
	// Simple check for comma-separated values
	for i := 0; i < len(varyHeader); {
		// Skip leading spaces
		for i < len(varyHeader) && varyHeader[i] == ' ' {
			i++
		}
		// Find end of token
		start := i
		for i < len(varyHeader) && varyHeader[i] != ',' {
			i++
		}
		token := varyHeader[start:i]
		// Trim trailing spaces
		for len(token) > 0 && token[len(token)-1] == ' ' {
			token = token[:len(token)-1]
		}
		if token == field {
			return true
		}
		// Skip comma
		if i < len(varyHeader) {
			i++
		}
	}
	return false
}

// corsMiddleware returns a Gin middleware for CORS
func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Check if origin is in allowed list
		allowed := false
		for _, allowedOrigin := range s.config.Server.AllowedOrigins {
			if origin == allowedOrigin {
				allowed = true
				break
			}
		}

		if allowed {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key, accept, origin, Cache-Control, X-Requested-With")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")
		}

		// Add Origin to Vary header (merge with existing values)
		varyHeader := c.Writer.Header().Get("Vary")
		if varyHeader == "" {
			c.Writer.Header().Set("Vary", "Origin")
		} else if varyHeader != "*" {
			// Only add if not already present
			if varyHeader != "Origin" && !containsVary(varyHeader, "Origin") {
				c.Writer.Header().Set("Vary", varyHeader+", Origin")
			}
		}

		if c.Request.Method == "OPTIONS" {
			if !allowed {
				// Reject preflight from non-allowed origins
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// setupRoutes configures API routes
func (s *Server) setupRoutes() {
	// Health check endpoint
	s.router.GET("/healthz", s.healthCheck)
	s.router.GET("/readyz", s.readinessCheck)

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	v1.Use(s.authMiddleware())
	{
		// VIP endpoints (to be implemented in Phase 3.2)
		vips := v1.Group("/vips")
		{
			vips.POST("", s.createVIP)
			vips.GET("", s.listVIPs)
			vips.GET("/:id", s.getVIP)
			vips.PUT("/:id", s.updateVIP)
			vips.DELETE("/:id", s.deleteVIP)
		}

		// Backend endpoints (to be implemented in Phase 3.3)
		backends := v1.Group("/backends")
		{
			backends.POST("", s.createBackend)
			backends.GET("", s.listBackends)
			backends.GET("/:id", s.getBackend)
			backends.PUT("/:id", s.updateBackend)
			backends.DELETE("/:id", s.deleteBackend)
		}

		// Revision endpoint (to be implemented in Phase 3.4)
		v1.GET("/revision", s.getRevision)
	}
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		expectedKey := s.config.Server.APIKey
		if expectedKey == "" {
			c.Next()
			return
		}

		if !apiKeyMatches(extractAPIKey(c.Request), expectedKey) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

func extractAPIKey(req *http.Request) string {
	if req == nil {
		return ""
	}
	if values := req.Header.Values("Authorization"); len(values) > 0 {
		for _, value := range values {
			fields := strings.Fields(value)
			if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
				return fields[1]
			}
		}
		return ""
	}
	return strings.TrimSpace(req.Header.Get("X-API-Key"))
}

func apiKeyMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

// healthCheck handles health check requests
func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// readinessCheck handles readiness check requests
func (s *Server) readinessCheck(c *gin.Context) {
	// Check datastore connectivity
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	// Try to get revision as a simple datastore health check
	_, err := s.datastore.GetRevision(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Datastore health check failed")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"error":  "datastore unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// getRevision handles GET /api/v1/revision
func (s *Server) getRevision(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	revision, err := s.datastore.GetRevision(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get revision")
		handleDataStoreError(c, err, "Revision")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"revision": revision,
	})
}

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)

	if s.config.Server.APIKey != "" && !s.config.Server.TLS {
		return fmt.Errorf("server.tls must be enabled when server.api_key is set")
	}

	s.httpServer = s.newHTTPServer(addr)

	s.logger.WithField("addr", addr).Info("Starting REST API server")

	var err error
	if s.config.Server.TLS {
		err = s.httpServer.ListenAndServeTLS(s.config.Server.CertFile, s.config.Server.KeyFile)
	} else {
		err = s.httpServer.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

func (s *Server) newHTTPServer(addr string) *http.Server {
	server := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadTimeout:       s.config.Server.ReadTimeout,
		WriteTimeout:      s.config.Server.WriteTimeout,
		ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
		IdleTimeout:       s.config.Server.IdleTimeout,
		MaxHeaderBytes:    s.config.Server.MaxHeaderBytes,
	}
	if s.config.Server.TLS {
		server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return server
}

// Shutdown gracefully shuts down the HTTP server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down REST API server")

	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}

	return nil
}
