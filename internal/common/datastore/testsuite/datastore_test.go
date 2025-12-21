// Package testsuite provides common tests for DataStore implementations
package testsuite

import (
	"context"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DataStoreFactory is a function that creates a DataStore instance for testing
type DataStoreFactory func(ctx context.Context, t *testing.T) (datastore.DataStore, func())

// RunDataStoreTests runs a comprehensive test suite for a DataStore implementation
func RunDataStoreTests(t *testing.T, factory DataStoreFactory) {
	t.Run("VIP CRUD", func(t *testing.T) {
		testVIPCRUD(t, factory)
	})

	t.Run("Backend CRUD", func(t *testing.T) {
		testBackendCRUD(t, factory)
	})

	t.Run("Revision Management", func(t *testing.T) {
		testRevisionManagement(t, factory)
	})

	t.Run("Transaction", func(t *testing.T) {
		testTransaction(t, factory)
	})

	t.Run("Watch", func(t *testing.T) {
		testWatch(t, factory)
	})

	t.Run("GetConfig", func(t *testing.T) {
		testGetConfig(t, factory)
	})
}

func testVIPCRUD(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Create VIP
	vip := &models.VIP{
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)
	assert.NotEmpty(t, vip.ID)

	// Get VIP
	retrieved, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, vip.ID, retrieved.ID)
	assert.Equal(t, vip.VIP, retrieved.VIP)
	assert.Equal(t, vip.Port, retrieved.Port)
	assert.Equal(t, vip.Protocol, retrieved.Protocol)
	assert.Equal(t, vip.LBMethod, retrieved.LBMethod)

	// List VIPs
	vips, err := ds.ListVIPs(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(vips), 1)
	found := false
	for _, v := range vips {
		if v.ID == vip.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "Created VIP should be in list")

	// Update VIP
	vip.Port = 443
	vip.Protocol = models.ProtocolUDP
	err = ds.UpdateVIP(ctx, vip)
	require.NoError(t, err)

	// Verify update
	updated, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, 443, updated.Port)
	assert.Equal(t, models.ProtocolUDP, updated.Protocol)
	// CreatedAt should be preserved
	assert.Equal(t, retrieved.CreatedAt, updated.CreatedAt)

	// Delete VIP
	err = ds.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = ds.GetVIP(ctx, vip.ID)
	assert.Error(t, err)
	assert.Equal(t, datastore.ErrNotFound, err)
}

func testBackendCRUD(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Create VIP first
	vip := &models.VIP{
		VIP:      "192.168.1.200",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// Add Backend
	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.1",
		Weight: 10,
	}

	err = ds.AddBackend(ctx, backend)
	require.NoError(t, err)
	assert.NotEmpty(t, backend.ID)

	// Get Backend
	retrieved, err := ds.GetBackend(ctx, backend.ID)
	require.NoError(t, err)
	assert.Equal(t, backend.ID, retrieved.ID)
	assert.Equal(t, backend.VIPID, retrieved.VIPID)
	assert.Equal(t, backend.IP, retrieved.IP)
	assert.Equal(t, backend.Weight, retrieved.Weight)

	// List Backends
	backends, err := ds.ListBackends(ctx, vip.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(backends), 1)
	found := false
	for _, b := range backends {
		if b.ID == backend.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "Created backend should be in list")

	// Update Backend
	backend.Weight = 20
	err = ds.UpdateBackend(ctx, backend)
	require.NoError(t, err)

	// Verify update
	updated, err := ds.GetBackend(ctx, backend.ID)
	require.NoError(t, err)
	assert.Equal(t, 20, updated.Weight)

	// Delete Backend
	err = ds.DeleteBackend(ctx, backend.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = ds.GetBackend(ctx, backend.ID)
	assert.Error(t, err)
	assert.Equal(t, datastore.ErrNotFound, err)
}

func testRevisionManagement(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Get initial revision
	initialRev, err := ds.GetRevision(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, initialRev, int64(0))

	// Create VIP should increment revision
	vip := &models.VIP{
		VIP:      "192.168.1.300",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	rev1, err := ds.GetRevision(ctx)
	require.NoError(t, err)
	assert.Greater(t, rev1, initialRev)

	// Increment revision explicitly
	rev2, err := ds.IncrementRevision(ctx)
	require.NoError(t, err)
	assert.Greater(t, rev2, rev1)

	// Verify revision is monotonic
	rev3, err := ds.GetRevision(ctx)
	require.NoError(t, err)
	assert.Equal(t, rev2, rev3)
}

func testTransaction(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Create VIP in transaction
	tx, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	vip := &models.VIP{
		VIP:      "192.168.1.400",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err = tx.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// VIP should not be visible before commit
	_, err = ds.GetVIP(ctx, vip.ID)
	assert.Error(t, err)
	assert.Equal(t, datastore.ErrNotFound, err)

	// Commit
	err = tx.Commit()
	require.NoError(t, err)

	// VIP should be visible after commit
	retrieved, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, vip.ID, retrieved.ID)

	// Test rollback
	tx2, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.2",
		Weight: 10,
	}

	err = tx2.AddBackend(ctx, backend)
	require.NoError(t, err)

	// Backend should not be visible before commit
	_, err = ds.GetBackend(ctx, backend.ID)
	assert.Error(t, err)

	// Rollback
	err = tx2.Rollback()
	require.NoError(t, err)

	// Backend should still not be visible after rollback
	_, err = ds.GetBackend(ctx, backend.ID)
	assert.Error(t, err)
}

func testWatch(t *testing.T, factory DataStoreFactory) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Start watching
	events, err := ds.Watch(ctx)
	require.NoError(t, err)

	// Create VIP
	vip := &models.VIP{
		VIP:      "192.168.1.500",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// Wait for event
	select {
	case event := <-events:
		assert.NotNil(t, event)
		assert.Equal(t, datastore.EventTypeVIPCreated, event.Type)
		assert.NotNil(t, event.VIP)
		assert.Equal(t, vip.ID, event.VIP.ID)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for watch event")
	}

	// Update VIP
	vip.Port = 443
	err = ds.UpdateVIP(ctx, vip)
	require.NoError(t, err)

	// Wait for update event
	select {
	case event := <-events:
		assert.NotNil(t, event)
		assert.Equal(t, datastore.EventTypeVIPUpdated, event.Type)
		assert.NotNil(t, event.VIP)
		assert.Equal(t, vip.ID, event.VIP.ID)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for watch event")
	}

	// Delete VIP
	err = ds.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	// Wait for delete event
	select {
	case event := <-events:
		assert.NotNil(t, event)
		assert.Equal(t, datastore.EventTypeVIPDeleted, event.Type)
		assert.NotNil(t, event.VIP)
		assert.Equal(t, vip.ID, event.VIP.ID)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for watch event")
	}
}

func testGetConfig(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Create VIP with health check and backends
	vip := &models.VIP{
		VIP:      "192.168.1.600",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
		HealthCheck: &models.HealthCheck{
			VIPID:       "", // Will be set after VIP creation
			Type:        models.HCTypeHTTP,
			IntervalSec: 10,
			TimeoutSec:  5,
			RiseCount:   3,
			FallCount:   3,
			Config: models.HCConfig{
				"port": 8080,
				"path": "/health",
			},
		},
	}

	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// Set VIPID in health check
	vip.HealthCheck.VIPID = vip.ID
	err = ds.UpdateVIP(ctx, vip)
	require.NoError(t, err)

	// Add backends
	backend1 := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.1",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, backend1)
	require.NoError(t, err)

	backend2 := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.2",
		Weight: 20,
	}
	err = ds.AddBackend(ctx, backend2)
	require.NoError(t, err)

	// Get config
	config, err := ds.GetConfig(ctx)
	require.NoError(t, err)
	assert.Greater(t, config.Revision, int64(0))

	// Find our VIP in config
	var foundVIP *models.VIPConfig
	for i := range config.VIPs {
		if config.VIPs[i].VIP.ID == vip.ID {
			foundVIP = &config.VIPs[i]
			break
		}
	}
	require.NotNil(t, foundVIP, "VIP should be in config")

	// Verify VIP details
	assert.Equal(t, vip.ID, foundVIP.VIP.ID)
	assert.Equal(t, vip.VIP, foundVIP.VIP.VIP)
	assert.Equal(t, vip.Port, foundVIP.VIP.Port)

	// Verify health check
	require.NotNil(t, foundVIP.HealthCheck)
	assert.Equal(t, models.HCTypeHTTP, foundVIP.HealthCheck.Type)
	assert.Equal(t, 10, foundVIP.HealthCheck.IntervalSec)

	// Verify backends
	assert.Len(t, foundVIP.Backends, 2)
	backendIDs := make(map[string]bool)
	for _, b := range foundVIP.Backends {
		backendIDs[b.ID] = true
	}
	assert.True(t, backendIDs[backend1.ID])
	assert.True(t, backendIDs[backend2.ID])
}

