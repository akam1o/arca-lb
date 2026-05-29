package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/akam1o/arca-lb/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer() (*Server, *testutil.MockDataStore) {
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Suppress logs during testing

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:             "localhost",
			Port:             8080,
			DataStoreTimeout: 5 * time.Second,
		},
		Log: config.LogConfig{
			Level: "error",
		},
	}

	mockDS := testutil.NewMockDataStore()
	server := NewServer(cfg, mockDS, logger)

	return server, mockDS
}

func TestValidateResourceID(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expectErr bool
	}{
		{
			name:  "simple id",
			value: "vip-1",
		},
		{
			name:  "max length",
			value: strings.Repeat("a", datastore.MaxResourceIDBytes),
		},
		{
			name:      "empty",
			value:     "",
			expectErr: true,
		},
		{
			name:      "space only",
			value:     " ",
			expectErr: true,
		},
		{
			name:      "contains slash",
			value:     "vip/1",
			expectErr: true,
		},
		{
			name:      "contains whitespace",
			value:     "vip 1",
			expectErr: true,
		},
		{
			name:      "contains control character",
			value:     "vip\x001",
			expectErr: true,
		},
		{
			name:      "too long",
			value:     strings.Repeat("a", datastore.MaxResourceIDBytes+1),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResourceID("id", tt.value)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestRequestBodyLimitRejectsOversizedCreateVIP(t *testing.T) {
	server, _ := setupTestServer()
	server.config.Server.MaxBodyBytes = 32

	body := `{"vip":"192.168.1.100","port":80,"protocol":"TCP"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, w.Body.String(), "request body too large")
}

func TestAPIKeyAuthMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		setHeader      func(*http.Request)
		expectedStatus int
	}{
		{
			name:           "missing key",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong bearer key",
			setHeader: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer wrong-controller-key")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "bearer key",
			setHeader: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer controller-secret")
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "case-insensitive bearer scheme",
			setHeader: func(req *http.Request) {
				req.Header.Set("Authorization", "bearer   controller-secret")
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "x api key",
			setHeader: func(req *http.Request) {
				req.Header.Set("X-API-Key", "controller-secret")
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "malformed authorization does not fall back to x api key",
			setHeader: func(req *http.Request) {
				req.Header.Set("Authorization", "Basic controller-secret")
				req.Header.Set("X-API-Key", "controller-secret")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "multiple authorization headers are rejected",
			setHeader: func(req *http.Request) {
				req.Header.Add("Authorization", "Bearer controller-secret")
				req.Header.Add("Authorization", "Bearer wrong-controller-key")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "multiple x api key headers are rejected",
			setHeader: func(req *http.Request) {
				req.Header.Add("X-API-Key", "controller-secret")
				req.Header.Add("X-API-Key", "wrong-controller-key")
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, mockDS := setupTestServer()
			server.config.Server.APIKey = "controller-secret"
			require.NoError(t, mockDS.CreateVIP(context.Background(), &models.VIP{
				ID:       "vip-auth",
				VIP:      "192.168.1.110",
				Port:     80,
				Protocol: models.ProtocolTCP,
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/vips/vip-auth", nil)
			if tt.setHeader != nil {
				tt.setHeader(req)
			}
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestAPIKeyAuthDoesNotProtectHealthEndpoints(t *testing.T) {
	server, _ := setupTestServer()
	server.config.Server.APIKey = "controller-secret"

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCreateVIP(t *testing.T) {
	server, mockDS := setupTestServer()

	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedStatus int
		setupMock      func()
	}{
		{
			name: "success",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "unknown field",
			requestBody: map[string]interface{}{
				"vip":       "192.168.1.100",
				"port":      80,
				"protocol":  "TCP",
				"encapType": "NAT4",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "with health check",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":       "HTTP",
					"interval":   "10s",
					"timeout":    "5s",
					"rise_count": 2,
					"fall_count": 4,
					"config": map[string]interface{}{
						"port":           8080,
						"path":           "/health",
						"expected_codes": []int{200, 204},
					},
				},
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "with health check seconds fields",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.103",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":             "HTTP",
					"interval_seconds": 12,
					"timeout_seconds":  4,
					"config": map[string]interface{}{
						"port": 8080,
						"path": "/healthz",
					},
				},
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "unknown health check field",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "HTTP",
					"interval": "10s",
					"timeout":  "5s",
					"probe":    "healthz",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check interval duration and seconds conflict",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":             "http",
					"interval":         "10s",
					"interval_seconds": 10,
					"timeout_seconds":  1,
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check mixed duration and seconds representations",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":             "http",
					"interval_seconds": 10,
					"timeout":          "1s",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check missing timing fields",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type": "http",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check timeout_seconds equals interval_seconds",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":             "http",
					"interval_seconds": 10,
					"timeout_seconds":  10,
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check interval_seconds exceeds int32",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":             "http",
					"interval_seconds": math.MaxInt32 + 1,
					"timeout_seconds":  1,
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "with tls hello health check",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.102",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "tls-hello",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port": 8443,
					},
				},
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "tls hello health check without port",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.102",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "tls-hello",
					"interval": "10s",
					"timeout":  "5s",
					"config":   map[string]interface{}{},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "invalid health check config",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"path": "/health",
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "fractional health check port",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port": 8080.5,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "fractional health check expected code",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port":           8080,
						"expected_codes": []float64{200.5},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check expected code below range",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port":           8080,
						"expected_codes": []int{99},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check expected code above range",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port":           8080,
						"expected_codes": []int{600},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check method must be string",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port":   8080,
						"method": 123,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check tcp send must be string",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "tcp",
					"interval": "10s",
					"timeout":  "5s",
					"config": map[string]interface{}{
						"port": 8080,
						"send": []string{"ping"},
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "sub-second health check interval",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "500ms",
					"timeout":  "1s",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check interval exceeds int32",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "2147483648s",
					"timeout":  "1s",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check rise count exceeds int32",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":       "http",
					"interval":   "10s",
					"timeout":    "1s",
					"rise_count": math.MaxInt32 + 1,
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "sub-second health check timeout",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "2s",
					"timeout":  "500ms",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check timeout equals interval",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "10s",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "health check timeout exceeds interval",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.101",
				"port":     443,
				"protocol": "TCP",
				"health_check": map[string]interface{}{
					"type":     "http",
					"interval": "10s",
					"timeout":  "11s",
					"config": map[string]interface{}{
						"port": 8080,
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "default LB method",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "invalid IP",
			requestBody: map[string]interface{}{
				"vip":      "invalid-ip",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "invalid port",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     70000,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "datastore conflict",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusConflict,
			setupMock: func() {
				mockDS.SetCreateVIPError(datastore.ErrConflict)
			},
		},
		{
			name: "datastore invalid input",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusBadRequest,
			setupMock: func() {
				mockDS.SetCreateVIPError(datastore.ErrInvalidInput)
			},
		},
		{
			name: "invalid dscp for L3DSR",
			requestBody: map[string]interface{}{
				"vip":        "192.168.1.100",
				"port":       80,
				"protocol":   "TCP",
				"encap_type": "L3DSR",
				"dscp":       0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "invalid dscp for default L3DSR",
			requestBody: map[string]interface{}{
				"vip":      "192.168.1.100",
				"port":     80,
				"protocol": "TCP",
				"dscp":     0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "IPv6 VIP with default L3DSR",
			requestBody: map[string]interface{}{
				"vip":      "2001:db8::100",
				"port":     80,
				"protocol": "TCP",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "IPv6 VIP with NAT4",
			requestBody: map[string]interface{}{
				"vip":        "2001:db8::100",
				"port":       80,
				"protocol":   "TCP",
				"encap_type": "NAT4",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "IPv4 VIP with NAT6",
			requestBody: map[string]interface{}{
				"vip":        "192.168.1.100",
				"port":       80,
				"protocol":   "TCP",
				"encap_type": "NAT6",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock
			mockDS = testutil.NewMockDataStore()
			server.datastore = mockDS

			if tt.setupMock != nil {
				tt.setupMock()
			}

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/vips", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusCreated {
				var vip models.VIP
				err := json.Unmarshal(w.Body.Bytes(), &vip)
				require.NoError(t, err)
				assert.NotEmpty(t, vip.ID)
				assert.Equal(t, tt.requestBody["vip"], vip.VIP)
				assert.Equal(t, tt.requestBody["port"], vip.Port)
				// Check default LB method
				if tt.name == "default LB method" {
					assert.Equal(t, models.LBMethodMaglev, vip.LBMethod)
				}
				if tt.name == "with health check" {
					require.NotNil(t, vip.HealthCheck)
					assert.Equal(t, models.HCTypeHTTP, vip.HealthCheck.Type)
					assert.Equal(t, 10, vip.HealthCheck.IntervalSec)
					assert.Equal(t, 5, vip.HealthCheck.TimeoutSec)
					assert.Equal(t, 2, vip.HealthCheck.RiseCount)
					assert.Equal(t, 4, vip.HealthCheck.FallCount)
				}
				if tt.name == "with health check seconds fields" {
					require.NotNil(t, vip.HealthCheck)
					assert.Equal(t, models.HCTypeHTTP, vip.HealthCheck.Type)
					assert.Equal(t, 12, vip.HealthCheck.IntervalSec)
					assert.Equal(t, 4, vip.HealthCheck.TimeoutSec)
					assert.Equal(t, 3, vip.HealthCheck.RiseCount)
					assert.Equal(t, 2, vip.HealthCheck.FallCount)
				}
				if tt.name == "with tls hello health check" {
					require.NotNil(t, vip.HealthCheck)
					assert.Equal(t, models.HCTypeTLSHello, vip.HealthCheck.Type)
					assert.Equal(t, 10, vip.HealthCheck.IntervalSec)
					assert.Equal(t, 5, vip.HealthCheck.TimeoutSec)
					assert.Equal(t, 3, vip.HealthCheck.RiseCount)
					assert.Equal(t, 2, vip.HealthCheck.FallCount)
				}
			}
		})
	}
}

func TestCreateVIPRejectsDuplicateJSONFields(t *testing.T) {
	server, _ := setupTestServer()
	body := `{"vip":"192.168.1.100","port":80,"protocol":"TCP","encap_type":"NAT4","encap_type":"GRE4"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, `duplicate field "encap_type"`, response["error"])
}

func TestCreateVIPRejectsDuplicateNestedJSONFields(t *testing.T) {
	server, _ := setupTestServer()
	body := `{"vip":"192.168.1.100","port":80,"protocol":"TCP","encap_type":"NAT4","health_check":{"type":"HTTP","interval":"10s","timeout":"5s","config":{"port":8080,"port":8081}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, `duplicate field "health_check.config.port"`, response["error"])
}

func TestListVIPs(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create test VIPs
	vip1 := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	vip2 := &models.VIP{
		ID:       "vip-2",
		VIP:      "192.168.1.101",
		Port:     443,
		Protocol: models.ProtocolTCP,
	}

	require.NoError(t, mockDS.CreateVIP(ctx, vip1))
	require.NoError(t, mockDS.CreateVIP(ctx, vip2))

	tests := []struct {
		name           string
		setupMock      func()
		expectedStatus int
		expectedCount  int
	}{
		{
			name:           "success",
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name: "datastore error",
			setupMock: func() {
				mockDS.SetListVIPsError(datastore.ErrInvalidInput)
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMock != nil {
				tt.setupMock()
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/vips", nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response, "vips")
				assert.Contains(t, response, "count")
				if tt.expectedCount >= 0 {
					assert.Equal(t, float64(tt.expectedCount), response["count"])
				}
			}
		})
	}
}

func TestGetVIP(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	tests := []struct {
		name           string
		vipID          string
		expectedStatus int
		setupMock      func()
	}{
		{
			name:           "success",
			vipID:          "vip-1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "not found",
			vipID:          "vip-nonexistent",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "datastore error",
			vipID:          "vip-1",
			expectedStatus: http.StatusBadRequest,
			setupMock: func() {
				mockDS.SetGetVIPError(datastore.ErrInvalidInput)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMock != nil {
				tt.setupMock()
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/vips/"+tt.vipID, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var vip models.VIP
				err := json.Unmarshal(w.Body.Bytes(), &vip)
				require.NoError(t, err)
				assert.Equal(t, tt.vipID, vip.ID)
			}
		})
	}
}

func TestUpdateVIP(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	tests := []struct {
		name           string
		vipID          string
		requestBody    interface{}
		expectedStatus int
		setupMock      func()
	}{
		{
			name:  "success",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"port": 8080,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:  "not found",
			vipID: "vip-nonexistent",
			requestBody: map[string]interface{}{
				"port": 8080,
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:  "invalid input",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"port": 70000,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "unknown field",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"encapType": "NAT4",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "zero port",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"port": 0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "null port",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"port": nil,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "empty protocol",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"protocol": "",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "null body",
			vipID:          "vip-1",
			requestBody:    nil,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "invalid dscp for default L3DSR",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"dscp": 0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "datastore error",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"port": 8080,
			},
			expectedStatus: http.StatusBadRequest,
			setupMock: func() {
				mockDS.SetUpdateVIPError(datastore.ErrInvalidInput)
			},
		},
		{
			name:  "invalid dscp for L3DSR",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"encap_type": "L3DSR",
				"dscp":       0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "IPv6 VIP with default L3DSR",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"vip": "2001:db8::100",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:  "IPv4 VIP with NAT6",
			vipID: "vip-1",
			requestBody: map[string]interface{}{
				"encap_type": "NAT6",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMock != nil {
				tt.setupMock()
			}

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/"+tt.vipID, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var updatedVIP models.VIP
				err := json.Unmarshal(w.Body.Bytes(), &updatedVIP)
				require.NoError(t, err)
				assert.Equal(t, tt.vipID, updatedVIP.ID)
				if requestBody, ok := tt.requestBody.(map[string]interface{}); ok {
					if port, ok := requestBody["port"].(int); ok {
						assert.Equal(t, port, updatedVIP.Port)
					}
				}
			}
		})
	}
}

func TestUpdateVIPRejectsDuplicateJSONFields(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	require.NoError(t, mockDS.CreateVIP(ctx, &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}))

	body := `{"port":8080,"port":8081}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/vip-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, `duplicate field "port"`, response["error"])
}

func TestUpdateVIPClearsDSCP(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	dscp := uint8(10)
	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.168.1.100",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		EncapType: models.EncapTypeL3DSR,
		DSCP:      &dscp,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	body, err := json.Marshal(map[string]interface{}{
		"dscp": nil,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/vip-1", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var updatedVIP models.VIP
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updatedVIP))
	require.Nil(t, updatedVIP.DSCP)

	storedVIP, err := mockDS.GetVIP(ctx, "vip-1")
	require.NoError(t, err)
	require.Nil(t, storedVIP.DSCP)
}

func TestUpdateVIPUpdatesHealthCheck(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
		HealthCheck: &models.HealthCheck{
			ID:          "hc-1",
			VIPID:       "vip-1",
			Type:        models.HCTypeTCP,
			IntervalSec: 10,
			TimeoutSec:  5,
			RiseCount:   3,
			FallCount:   2,
			Config:      models.HCConfig{"port": float64(8080)},
		},
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	body, err := json.Marshal(map[string]interface{}{
		"health_check": map[string]interface{}{
			"type":             "HTTP",
			"interval_seconds": 15,
			"timeout_seconds":  3,
			"rise_count":       4,
			"fall_count":       5,
			"config":           map[string]interface{}{"port": 8081, "path": "/readyz"},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/vip-1", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var updatedVIP models.VIP
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updatedVIP))
	require.NotNil(t, updatedVIP.HealthCheck)
	assert.Equal(t, models.HCTypeHTTP, updatedVIP.HealthCheck.Type)
	assert.Equal(t, 15, updatedVIP.HealthCheck.IntervalSec)
	assert.Equal(t, 3, updatedVIP.HealthCheck.TimeoutSec)
	assert.Equal(t, 4, updatedVIP.HealthCheck.RiseCount)
	assert.Equal(t, 5, updatedVIP.HealthCheck.FallCount)

	storedVIP, err := mockDS.GetVIP(ctx, "vip-1")
	require.NoError(t, err)
	require.NotNil(t, storedVIP.HealthCheck)
	assert.Equal(t, models.HCTypeHTTP, storedVIP.HealthCheck.Type)
	assert.Equal(t, 15, storedVIP.HealthCheck.IntervalSec)
	assert.Equal(t, 3, storedVIP.HealthCheck.TimeoutSec)
}

func TestUpdateVIPClearsHealthCheck(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
		HealthCheck: &models.HealthCheck{
			ID:          "hc-1",
			VIPID:       "vip-1",
			Type:        models.HCTypeHTTP,
			IntervalSec: 10,
			TimeoutSec:  5,
			RiseCount:   3,
			FallCount:   2,
			Config:      models.HCConfig{"port": float64(8080)},
		},
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	body, err := json.Marshal(map[string]interface{}{
		"health_check": nil,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/vip-1", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var updatedVIP models.VIP
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updatedVIP))
	require.Nil(t, updatedVIP.HealthCheck)

	storedVIP, err := mockDS.GetVIP(ctx, "vip-1")
	require.NoError(t, err)
	require.Nil(t, storedVIP.HealthCheck)
}

func TestUpdateVIPRejectsExistingBackendAddressFamilyMismatch(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.168.1.100",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		EncapType: models.EncapTypeGRE4,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))
	require.NoError(t, mockDS.AddBackend(ctx, &models.Backend{
		ID:     "backend-1",
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 1,
	}))

	body, err := json.Marshal(map[string]interface{}{
		"encap_type": "GRE6",
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vips/vip-1", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "existing backend backend-1")
	assert.Contains(t, w.Body.String(), "backend ip must be IPv6 when encap_type is GRE6")

	storedVIP, err := mockDS.GetVIP(ctx, "vip-1")
	require.NoError(t, err)
	assert.Equal(t, models.EncapTypeGRE4, storedVIP.EncapType)
}

func TestDeleteVIP(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	// Create backend for this VIP
	backend := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 10,
	}
	require.NoError(t, mockDS.AddBackend(ctx, backend))

	tests := []struct {
		name                 string
		vipID                string
		expectedStatus       int
		setupMock            func()
		verifyBackendDeleted bool
	}{
		{
			name:                 "success",
			vipID:                "vip-1",
			expectedStatus:       http.StatusNoContent,
			verifyBackendDeleted: true,
		},
		{
			name:           "not found",
			vipID:          "vip-nonexistent",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "datastore error",
			vipID:          "vip-1",
			expectedStatus: http.StatusBadRequest,
			setupMock: func() {
				mockDS.SetDeleteVIPError(datastore.ErrInvalidInput)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Recreate VIP and backend for each test
			require.NoError(t, mockDS.CreateVIP(ctx, vip))
			require.NoError(t, mockDS.AddBackend(ctx, backend))

			if tt.setupMock != nil {
				tt.setupMock()
			}

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/vips/"+tt.vipID, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusNoContent {
				assert.Empty(t, w.Body.String())
			}

			if tt.verifyBackendDeleted && tt.expectedStatus == http.StatusNoContent {
				// Verify backend is also deleted
				_, err := mockDS.GetBackend(ctx, backend.ID)
				assert.Error(t, err)
				assert.Equal(t, datastore.ErrNotFound, err)
			}
		})
	}
}
