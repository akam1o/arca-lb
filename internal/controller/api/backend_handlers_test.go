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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBackend(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create a VIP first
	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))
	vip6 := &models.VIP{
		ID:        "vip-6",
		VIP:       "2001:db8::100",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		EncapType: models.EncapTypeNAT6,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip6))

	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedStatus int
		setupMock      func()
	}{
		{
			name: "success",
			requestBody: map[string]interface{}{
				"vip_id": "vip-1",
				"ip":     "10.0.0.1",
				"weight": 10,
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "default weight",
			requestBody: map[string]interface{}{
				"vip_id": "vip-1",
				"ip":     "10.0.0.2",
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "unknown field",
			requestBody: map[string]interface{}{
				"vip_id":      "vip-1",
				"ip":          "10.0.0.2",
				"monitor_ip":  "10.0.0.3",
				"monitorPort": 8080,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "VIP not found",
			requestBody: map[string]interface{}{
				"vip_id": "vip-nonexistent",
				"ip":     "10.0.0.1",
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "malformed VIP id",
			requestBody: map[string]interface{}{
				"vip_id": "vip/1",
				"ip":     "10.0.0.1",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "invalid IP",
			requestBody: map[string]interface{}{
				"vip_id": "vip-1",
				"ip":     "invalid-ip",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "IPv6 backend for default L3DSR VIP",
			requestBody: map[string]interface{}{
				"vip_id": "vip-1",
				"ip":     "2001:db8::10",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "IPv6 backend for NAT6 VIP",
			requestBody: map[string]interface{}{
				"vip_id": "vip-6",
				"ip":     "2001:db8::10",
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "IPv4 backend for NAT6 VIP",
			requestBody: map[string]interface{}{
				"vip_id": "vip-6",
				"ip":     "10.0.0.1",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "datastore conflict",
			requestBody: map[string]interface{}{
				"vip_id": "vip-1",
				"ip":     "10.0.0.1",
			},
			expectedStatus: http.StatusConflict,
			setupMock: func() {
				mockDS.SetCreateBackendError(datastore.ErrConflict)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock if needed
			if tt.setupMock != nil {
				// Recreate VIP
				require.NoError(t, mockDS.CreateVIP(ctx, vip))
				tt.setupMock()
			}

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/backends", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusCreated {
				var backend models.Backend
				err := json.Unmarshal(w.Body.Bytes(), &backend)
				require.NoError(t, err)
				assert.NotEmpty(t, backend.ID)
				assert.Equal(t, tt.requestBody["ip"], backend.IP)
			}
		})
	}
}

func TestListBackends(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create VIP and backends
	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	backend1 := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 10,
	}
	backend2 := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.2",
		Weight: 20,
	}

	require.NoError(t, mockDS.AddBackend(ctx, backend1))
	require.NoError(t, mockDS.AddBackend(ctx, backend2))

	tests := []struct {
		name           string
		vipID          string
		expectedStatus int
		expectedCount  int
	}{
		{
			name:           "success",
			vipID:          "vip-1",
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:           "empty list",
			vipID:          "vip-nonexistent",
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
		{
			name:           "missing vip_id",
			vipID:          "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed vip_id",
			vipID:          "vip/1",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v1/backends"
			if tt.vipID != "" {
				url += "?vip_id=" + tt.vipID
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response, "backends")
				assert.Contains(t, response, "count")
				if tt.expectedCount >= 0 {
					assert.Equal(t, float64(tt.expectedCount), response["count"])
				}
			}
		})
	}
}

func TestGetBackend(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create VIP and backend
	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	backend := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 10,
	}
	require.NoError(t, mockDS.AddBackend(ctx, backend))

	tests := []struct {
		name           string
		backendID      string
		expectedStatus int
	}{
		{
			name:           "success",
			backendID:      backend.ID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "not found",
			backendID:      "backend-nonexistent",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/backends/"+tt.backendID, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var backend models.Backend
				err := json.Unmarshal(w.Body.Bytes(), &backend)
				require.NoError(t, err)
				assert.Equal(t, tt.backendID, backend.ID)
			}
		})
	}
}

func TestUpdateBackend(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create VIP and backend
	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))
	vip6 := &models.VIP{
		ID:        "vip-6",
		VIP:       "2001:db8::100",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		EncapType: models.EncapTypeNAT6,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip6))

	backend := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 10,
	}
	require.NoError(t, mockDS.AddBackend(ctx, backend))
	backend6 := &models.Backend{
		VIPID:  "vip-6",
		IP:     "2001:db8::10",
		Weight: 10,
	}
	require.NoError(t, mockDS.AddBackend(ctx, backend6))

	tests := []struct {
		name           string
		backendID      string
		requestBody    interface{}
		expectedStatus int
	}{
		{
			name:      "success",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"weight": 20,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:      "not found",
			backendID: "backend-nonexistent",
			requestBody: map[string]interface{}{
				"weight": 20,
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "invalid weight",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"weight": 200,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "unknown field",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"monitor_ip": "10.0.0.2",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "zero weight",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"weight": 0,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "null weight",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"weight": nil,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "empty ip",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"ip": "",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "null body",
			backendID:      backend.ID,
			requestBody:    nil,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "IPv6 backend for default L3DSR VIP",
			backendID: backend.ID,
			requestBody: map[string]interface{}{
				"ip": "2001:db8::10",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "IPv4 backend for NAT6 VIP",
			backendID: backend6.ID,
			requestBody: map[string]interface{}{
				"ip": "10.0.0.1",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/backends/"+tt.backendID, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestDeleteBackend(t *testing.T) {
	server, mockDS := setupTestServer()
	ctx := context.TODO()

	// Create VIP and backend
	vip := &models.VIP{
		ID:       "vip-1",
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
	}
	require.NoError(t, mockDS.CreateVIP(ctx, vip))

	backend := &models.Backend{
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 10,
	}
	require.NoError(t, mockDS.AddBackend(ctx, backend))

	tests := []struct {
		name           string
		backendID      string
		expectedStatus int
	}{
		{
			name:           "success",
			backendID:      backend.ID,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "not found",
			backendID:      "backend-nonexistent",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/backends/"+tt.backendID, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}
