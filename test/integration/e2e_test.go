//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	controllerBaseURL = "http://localhost:8080"
	healthzEndpoint   = "/healthz"
	readyzEndpoint    = "/readyz"
	vipsEndpoint      = "/api/v1/vips"
	backendsEndpoint  = "/api/v1/backends"
	revisionEndpoint  = "/api/v1/revision"
)

// TestE2E_HealthChecks tests the health check endpoints
func TestE2E_HealthChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Test /healthz
	resp, err := http.Get(controllerBaseURL + healthzEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "healthy", healthResp["status"])

	// Test /readyz
	resp, err = http.Get(controllerBaseURL + readyzEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var readyResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&readyResp)
	require.NoError(t, err)
	assert.Equal(t, "ready", readyResp["status"])
}

// TestE2E_VIPLifecycle tests the complete VIP lifecycle
func TestE2E_VIPLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()

	// Create VIP
	createReq := map[string]interface{}{
		"vip":      "192.168.1.100",
		"port":     80,
		"protocol": "TCP",
		"lb_method": "maglev",
		"health_check": map[string]interface{}{
			"type":         "http",
			"interval_sec": 10,
			"timeout_sec":  5,
			"rise_count":   3,
			"fall_count":   3,
			"config": map[string]interface{}{
				"port": 8080,
				"path": "/health",
			},
		},
	}

	reqBody, err := json.Marshal(createReq)
	require.NoError(t, err)

	resp, err := http.Post(controllerBaseURL+vipsEndpoint, "application/json", bytes.NewBuffer(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdVIP models.VIP
	err = json.NewDecoder(resp.Body).Decode(&createdVIP)
	require.NoError(t, err)
	assert.NotEmpty(t, createdVIP.ID)
	assert.Equal(t, "192.168.1.100", createdVIP.VIP)
	assert.Equal(t, 80, createdVIP.Port)

	vipID := createdVIP.ID

	// List VIPs
	resp, err = http.Get(controllerBaseURL + vipsEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var listResp struct {
		VIPs  []models.VIP `json:"vips"`
		Count int           `json:"count"`
	}
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, listResp.Count, 1)

	// Get VIP
	resp, err = http.Get(fmt.Sprintf("%s%s/%s", controllerBaseURL, vipsEndpoint, vipID))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var retrievedVIP models.VIP
	err = json.NewDecoder(resp.Body).Decode(&retrievedVIP)
	require.NoError(t, err)
	assert.Equal(t, vipID, retrievedVIP.ID)

	// Update VIP
	updateReq := map[string]interface{}{
		"port":     443,
		"protocol": "TCP",
	}
	updateBody, err := json.Marshal(updateReq)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s%s/%s", controllerBaseURL, vipsEndpoint, vipID), bytes.NewBuffer(updateBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var updatedVIP models.VIP
	err = json.NewDecoder(resp.Body).Decode(&updatedVIP)
	require.NoError(t, err)
	assert.Equal(t, 443, updatedVIP.Port)

	// Add Backend
	backendReq := map[string]interface{}{
		"vip_id": vipID,
		"ip":     "10.0.0.1",
		"weight": 10,
	}
	backendBody, err := json.Marshal(backendReq)
	require.NoError(t, err)

	resp, err = http.Post(controllerBaseURL+backendsEndpoint, "application/json", bytes.NewBuffer(backendBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdBackend models.Backend
	err = json.NewDecoder(resp.Body).Decode(&createdBackend)
	require.NoError(t, err)
	assert.NotEmpty(t, createdBackend.ID)
	assert.Equal(t, vipID, createdBackend.VIPID)
	assert.Equal(t, "10.0.0.1", createdBackend.IP)

	backendID := createdBackend.ID

	// List Backends
	resp, err = http.Get(fmt.Sprintf("%s%s?vip_id=%s", controllerBaseURL, backendsEndpoint, vipID))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var backendListResp struct {
		Backends []models.Backend `json:"backends"`
		Count    int              `json:"count"`
	}
	err = json.NewDecoder(resp.Body).Decode(&backendListResp)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, backendListResp.Count, 1)

	// Get Revision
	resp, err = http.Get(controllerBaseURL + revisionEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var revisionResp struct {
		Revision int64 `json:"revision"`
	}
	err = json.NewDecoder(resp.Body).Decode(&revisionResp)
	require.NoError(t, err)
	assert.Greater(t, revisionResp.Revision, int64(0))

	// Delete Backend
	req, err = http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s%s/%s", controllerBaseURL, backendsEndpoint, backendID), nil)
	require.NoError(t, err)

	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Delete VIP
	req, err = http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s%s/%s", controllerBaseURL, vipsEndpoint, vipID), nil)
	require.NoError(t, err)

	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify deletion
	resp, err = http.Get(fmt.Sprintf("%s%s/%s", controllerBaseURL, vipsEndpoint, vipID))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestE2E_RevisionIncrement tests that revision increments on changes
func TestE2E_RevisionIncrement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Get initial revision
	resp, err := http.Get(controllerBaseURL + revisionEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var initialRev struct {
		Revision int64 `json:"revision"`
	}
	err = json.NewDecoder(resp.Body).Decode(&initialRev)
	require.NoError(t, err)

	// Create VIP
	createReq := map[string]interface{}{
		"vip":      "192.168.1.200",
		"port":     80,
		"protocol": "TCP",
		"lb_method": "maglev",
	}
	reqBody, err := json.Marshal(createReq)
	require.NoError(t, err)

	resp, err = http.Post(controllerBaseURL+vipsEndpoint, "application/json", bytes.NewBuffer(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var createdVIP models.VIP
	err = json.NewDecoder(resp.Body).Decode(&createdVIP)
	require.NoError(t, err)

	// Get revision after create
	resp, err = http.Get(controllerBaseURL + revisionEndpoint)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var revAfterCreate struct {
		Revision int64 `json:"revision"`
	}
	err = json.NewDecoder(resp.Body).Decode(&revAfterCreate)
	require.NoError(t, err)
	assert.Greater(t, revAfterCreate.Revision, initialRev.Revision)

	// Cleanup
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s%s/%s", controllerBaseURL, vipsEndpoint, createdVIP.ID), nil)
	require.NoError(t, err)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err = client.Do(req)
	require.NoError(t, err)
	if resp.Body != nil {
		resp.Body.Close()
	}
}

// TestE2E_ErrorHandling tests error handling scenarios
func TestE2E_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Test invalid VIP creation
	invalidReq := map[string]interface{}{
		"vip":      "invalid-ip",
		"port":     80,
		"protocol": "TCP",
	}
	reqBody, err := json.Marshal(invalidReq)
	require.NoError(t, err)

	resp, err := http.Post(controllerBaseURL+vipsEndpoint, "application/json", bytes.NewBuffer(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Test non-existent VIP
	resp, err = http.Get(fmt.Sprintf("%s%s/non-existent-id", controllerBaseURL, vipsEndpoint))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Test backend creation with non-existent VIP
	backendReq := map[string]interface{}{
		"vip_id": "non-existent-vip-id",
		"ip":     "10.0.0.1",
		"weight": 10,
	}
	backendBody, err := json.Marshal(backendReq)
	require.NoError(t, err)

	resp, err = http.Post(controllerBaseURL+backendsEndpoint, "application/json", bytes.NewBuffer(backendBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

