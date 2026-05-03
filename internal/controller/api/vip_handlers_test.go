package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
			Host: "localhost",
			Port: 8080,
		},
		Log: config.LogConfig{
			Level: "error",
		},
	}

	mockDS := testutil.NewMockDataStore()
	server := NewServer(cfg, mockDS, logger)

	return server, mockDS
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
			}
		})
	}
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
		requestBody    map[string]interface{}
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
				if port, ok := tt.requestBody["port"].(int); ok {
					assert.Equal(t, port, updatedVIP.Port)
				}
			}
		})
	}
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
			expectedStatus:       http.StatusOK,
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

			if tt.verifyBackendDeleted && tt.expectedStatus == http.StatusOK {
				// Verify backend is also deleted
				_, err := mockDS.GetBackend(ctx, backend.ID)
				assert.Error(t, err)
				assert.Equal(t, datastore.ErrNotFound, err)
			}
		})
	}
}
