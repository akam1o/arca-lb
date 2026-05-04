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

	t.Run("VIP Nullable Fields", func(t *testing.T) {
		testVIPNullableFields(t, factory)
	})

	t.Run("VIP Uniqueness", func(t *testing.T) {
		testVIPUniqueness(t, factory)
	})

	t.Run("Backend CRUD", func(t *testing.T) {
		testBackendCRUD(t, factory)
	})

	t.Run("Backend Uniqueness", func(t *testing.T) {
		testBackendUniqueness(t, factory)
	})

	t.Run("Revision Management", func(t *testing.T) {
		testRevisionManagement(t, factory)
	})

	t.Run("Transaction", func(t *testing.T) {
		testTransaction(t, factory)
	})

	t.Run("Transaction VIP Tuple Index", func(t *testing.T) {
		testTransactionVIPTupleIndex(t, factory)
	})

	t.Run("Watch", func(t *testing.T) {
		testWatch(t, factory)
	})

	t.Run("Watch Does Not Replay Existing Changes", func(t *testing.T) {
		testWatchDoesNotReplayExistingChanges(t, factory)
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

	// Creating another VIP with the same ID should not overwrite the original.
	duplicateVIP := &models.VIP{
		ID:       vip.ID,
		VIP:      "192.168.1.101",
		Port:     81,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, duplicateVIP)
	require.ErrorIs(t, err, datastore.ErrConflict)
	retrieved, err = ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, vip.VIP, retrieved.VIP)
	assert.Equal(t, vip.Port, retrieved.Port)

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

	// Deleting a missing VIP should report not found consistently.
	err = ds.DeleteVIP(ctx, vip.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
}

func testVIPNullableFields(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	dscp := uint8(10)
	vip := &models.VIP{
		VIP:       "192.168.1.120",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		LBMethod:  models.LBMethodMaglev,
		EncapType: models.EncapTypeL3DSR,
		DSCP:      &dscp,
	}

	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	vip.EncapType = ""
	vip.DSCP = nil
	err = ds.UpdateVIP(ctx, vip)
	require.NoError(t, err)

	updated, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.EncapType)
	assert.Nil(t, updated.DSCP)

	nextDSCP := uint8(20)
	updated.EncapType = models.EncapTypeGRE4
	updated.DSCP = &nextDSCP
	err = ds.UpdateVIP(ctx, updated)
	require.NoError(t, err)

	tx, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	updated.EncapType = ""
	updated.DSCP = nil
	err = tx.UpdateVIP(ctx, updated)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	updated, err = ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.EncapType)
	assert.Nil(t, updated.DSCP)
}

func testVIPUniqueness(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	vip := &models.VIP{
		VIP:      "192.168.1.130",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	duplicate := &models.VIP{
		VIP:      vip.VIP,
		Port:     vip.Port,
		Protocol: vip.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, duplicate)
	require.ErrorIs(t, err, datastore.ErrConflict)

	other := &models.VIP{
		VIP:      "192.168.1.131",
		Port:     81,
		Protocol: models.ProtocolUDP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, other)
	require.NoError(t, err)

	other.VIP = vip.VIP
	other.Port = vip.Port
	other.Protocol = vip.Protocol
	err = ds.UpdateVIP(ctx, other)
	require.ErrorIs(t, err, datastore.ErrConflict)

	retrieved, err := ds.GetVIP(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.131", retrieved.VIP)
	assert.Equal(t, 81, retrieved.Port)
	assert.Equal(t, models.ProtocolUDP, retrieved.Protocol)

	err = ds.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	replacement := &models.VIP{
		VIP:      vip.VIP,
		Port:     vip.Port,
		Protocol: vip.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, replacement)
	require.NoError(t, err)
}

func testBackendCRUD(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	// Backend cannot be created without its parent VIP.
	missingParentBackend := &models.Backend{
		VIPID:  "missing-vip-id",
		IP:     "10.0.0.254",
		Weight: 1,
	}
	err := ds.AddBackend(ctx, missingParentBackend)
	require.ErrorIs(t, err, datastore.ErrNotFound)
	_, err = ds.GetBackend(ctx, missingParentBackend.ID)
	require.ErrorIs(t, err, datastore.ErrNotFound)

	// Create VIP first
	vip := &models.VIP{
		VIP:      "192.168.1.200",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, vip)
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

	// Reusing a backend ID must not move the reverse index to another VIP.
	otherVIP := &models.VIP{
		VIP:      "192.168.1.202",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, otherVIP)
	require.NoError(t, err)

	duplicateBackend := &models.Backend{
		ID:     backend.ID,
		VIPID:  otherVIP.ID,
		IP:     "10.0.0.9",
		Weight: 1,
	}
	err = ds.AddBackend(ctx, duplicateBackend)
	require.ErrorIs(t, err, datastore.ErrConflict)
	retrieved, err = ds.GetBackend(ctx, backend.ID)
	require.NoError(t, err)
	assert.Equal(t, backend.VIPID, retrieved.VIPID)
	assert.Equal(t, backend.IP, retrieved.IP)

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

	// Deleting a VIP should remove its backends as well.
	cascadeVIP := &models.VIP{
		VIP:      "192.168.1.201",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, cascadeVIP)
	require.NoError(t, err)

	cascadeBackend := &models.Backend{
		VIPID:  cascadeVIP.ID,
		IP:     "10.0.0.3",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, cascadeBackend)
	require.NoError(t, err)

	err = ds.DeleteVIP(ctx, cascadeVIP.ID)
	require.NoError(t, err)
	_, err = ds.GetBackend(ctx, cascadeBackend.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
}

func testBackendUniqueness(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	vip := &models.VIP{
		VIP:      "192.168.1.210",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.1.1",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, backend)
	require.NoError(t, err)

	duplicate := &models.Backend{
		VIPID:  vip.ID,
		IP:     backend.IP,
		Weight: 1,
	}
	err = ds.AddBackend(ctx, duplicate)
	require.ErrorIs(t, err, datastore.ErrConflict)

	other := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.1.2",
		Weight: 20,
	}
	err = ds.AddBackend(ctx, other)
	require.NoError(t, err)

	other.IP = backend.IP
	err = ds.UpdateBackend(ctx, other)
	require.ErrorIs(t, err, datastore.ErrConflict)

	retrieved, err := ds.GetBackend(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.2", retrieved.IP)
	assert.Equal(t, 20, retrieved.Weight)

	err = ds.DeleteBackend(ctx, backend.ID)
	require.NoError(t, err)

	replacement := &models.Backend{
		VIPID:  vip.ID,
		IP:     backend.IP,
		Weight: 1,
	}
	err = ds.AddBackend(ctx, replacement)
	require.NoError(t, err)
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
	originalCreatedAt := retrieved.CreatedAt

	txUpdateVIP, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	retrieved.Port = 8080
	err = txUpdateVIP.UpdateVIP(ctx, retrieved)
	require.NoError(t, err)
	err = txUpdateVIP.Commit()
	require.NoError(t, err)

	updatedVIP, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, 8080, updatedVIP.Port)
	assert.Equal(t, originalCreatedAt, updatedVIP.CreatedAt)

	txDuplicateVIP, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	duplicateVIP := &models.VIP{
		ID:       vip.ID,
		VIP:      "192.168.1.401",
		Port:     81,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = txDuplicateVIP.CreateVIP(ctx, duplicateVIP)
	if err == nil {
		err = txDuplicateVIP.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrConflict)
	_ = txDuplicateVIP.Rollback()

	updatedVIP, err = ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	assert.Equal(t, 8080, updatedVIP.Port)

	txDuplicateVIPTuple, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	duplicateVIPTuple := &models.VIP{
		VIP:      updatedVIP.VIP,
		Port:     updatedVIP.Port,
		Protocol: updatedVIP.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	err = txDuplicateVIPTuple.CreateVIP(ctx, duplicateVIPTuple)
	if err == nil {
		err = txDuplicateVIPTuple.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrConflict)
	_ = txDuplicateVIPTuple.Rollback()

	// A backend created in a transaction should be fully usable after commit.
	txBackend, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	committedBackend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.3",
		Weight: 10,
	}
	err = txBackend.AddBackend(ctx, committedBackend)
	require.NoError(t, err)

	_, err = ds.GetBackend(ctx, committedBackend.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)

	err = txBackend.Commit()
	require.NoError(t, err)

	retrievedBackend, err := ds.GetBackend(ctx, committedBackend.ID)
	require.NoError(t, err)
	assert.Equal(t, committedBackend.ID, retrievedBackend.ID)
	assert.Equal(t, committedBackend.VIPID, retrievedBackend.VIPID)

	txDuplicateBackend, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	duplicateBackend := &models.Backend{
		ID:     committedBackend.ID,
		VIPID:  vip.ID,
		IP:     "10.0.0.9",
		Weight: 1,
	}
	err = txDuplicateBackend.AddBackend(ctx, duplicateBackend)
	if err == nil {
		err = txDuplicateBackend.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrConflict)
	_ = txDuplicateBackend.Rollback()

	retrievedBackend, err = ds.GetBackend(ctx, committedBackend.ID)
	require.NoError(t, err)
	assert.Equal(t, committedBackend.VIPID, retrievedBackend.VIPID)
	assert.Equal(t, committedBackend.IP, retrievedBackend.IP)

	txDuplicateBackendIP, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	duplicateBackendIP := &models.Backend{
		VIPID:  vip.ID,
		IP:     committedBackend.IP,
		Weight: 1,
	}
	err = txDuplicateBackendIP.AddBackend(ctx, duplicateBackendIP)
	if err == nil {
		err = txDuplicateBackendIP.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrConflict)
	_ = txDuplicateBackendIP.Rollback()

	committedBackend.Weight = 20
	err = ds.UpdateBackend(ctx, committedBackend)
	require.NoError(t, err)

	err = ds.DeleteBackend(ctx, committedBackend.ID)
	require.NoError(t, err)

	// A transactional backend still requires an existing parent VIP.
	txMissingBackend, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	missingParentBackend := &models.Backend{
		VIPID:  "missing-vip-id",
		IP:     "10.0.0.254",
		Weight: 1,
	}
	err = txMissingBackend.AddBackend(ctx, missingParentBackend)
	if err == nil {
		err = txMissingBackend.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrNotFound)
	_ = txMissingBackend.Rollback()

	// A transactional VIP update must not create a missing VIP.
	txMissingVIPUpdate, err := ds.BeginTx(ctx)
	require.NoError(t, err)

	missingVIP := &models.VIP{
		ID:       "missing-vip-update-id-" + time.Now().Format("150405.000000000"),
		VIP:      "192.168.1.254",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = txMissingVIPUpdate.UpdateVIP(ctx, missingVIP)
	if err == nil {
		err = txMissingVIPUpdate.Commit()
	}
	require.ErrorIs(t, err, datastore.ErrNotFound)
	_, err = ds.GetVIP(ctx, missingVIP.ID)
	require.ErrorIs(t, err, datastore.ErrNotFound)
	_ = txMissingVIPUpdate.Rollback()

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

func testTransactionVIPTupleIndex(t *testing.T, factory DataStoreFactory) {
	ctx := context.Background()
	ds, cleanup := factory(ctx, t)
	defer cleanup()

	updatedTwice := &models.VIP{
		VIP:      "192.168.1.220",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, updatedTwice)
	require.NoError(t, err)

	txUpdateTwice, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	updatedTwice.Port = 81
	err = txUpdateTwice.UpdateVIP(ctx, updatedTwice)
	require.NoError(t, err)
	updatedTwice.Port = 82
	err = txUpdateTwice.UpdateVIP(ctx, updatedTwice)
	require.NoError(t, err)
	require.NoError(t, txUpdateTwice.Commit())

	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      updatedTwice.VIP,
		Port:     81,
		Protocol: updatedTwice.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)
	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      updatedTwice.VIP,
		Port:     82,
		Protocol: updatedTwice.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.ErrorIs(t, err, datastore.ErrConflict)

	reverted := &models.VIP{
		VIP:      "192.168.1.221",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, reverted)
	require.NoError(t, err)

	txRevert, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	reverted.Port = 81
	err = txRevert.UpdateVIP(ctx, reverted)
	require.NoError(t, err)
	reverted.Port = 80
	err = txRevert.UpdateVIP(ctx, reverted)
	require.NoError(t, err)
	require.NoError(t, txRevert.Commit())

	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      reverted.VIP,
		Port:     81,
		Protocol: reverted.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)
	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      reverted.VIP,
		Port:     80,
		Protocol: reverted.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.ErrorIs(t, err, datastore.ErrConflict)

	deletedAfterUpdate := &models.VIP{
		VIP:      "192.168.1.222",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, deletedAfterUpdate)
	require.NoError(t, err)

	txDeleteAfterUpdate, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	deletedAfterUpdate.Port = 81
	err = txDeleteAfterUpdate.UpdateVIP(ctx, deletedAfterUpdate)
	require.NoError(t, err)
	err = txDeleteAfterUpdate.DeleteVIP(ctx, deletedAfterUpdate.ID)
	require.NoError(t, err)
	require.NoError(t, txDeleteAfterUpdate.Commit())

	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      deletedAfterUpdate.VIP,
		Port:     80,
		Protocol: deletedAfterUpdate.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)
	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      deletedAfterUpdate.VIP,
		Port:     81,
		Protocol: deletedAfterUpdate.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)

	reusedAfterDelete := &models.VIP{
		VIP:      "192.168.1.223",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, reusedAfterDelete)
	require.NoError(t, err)
	deletedForReuse := &models.VIP{
		VIP:      "192.168.1.224",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, deletedForReuse)
	require.NoError(t, err)

	oldReusedAfterDeleteVIP := reusedAfterDelete.VIP
	txReuseAfterDelete, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	err = txReuseAfterDelete.DeleteVIP(ctx, deletedForReuse.ID)
	require.NoError(t, err)
	reusedAfterDelete.VIP = deletedForReuse.VIP
	reusedAfterDelete.Port = deletedForReuse.Port
	err = txReuseAfterDelete.UpdateVIP(ctx, reusedAfterDelete)
	require.NoError(t, err)
	require.NoError(t, txReuseAfterDelete.Commit())

	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      oldReusedAfterDeleteVIP,
		Port:     reusedAfterDelete.Port,
		Protocol: reusedAfterDelete.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)
	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      deletedForReuse.VIP,
		Port:     deletedForReuse.Port,
		Protocol: deletedForReuse.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.ErrorIs(t, err, datastore.ErrConflict)

	reusedAfterMove := &models.VIP{
		VIP:      "192.168.1.225",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, reusedAfterMove)
	require.NoError(t, err)
	movedForReuse := &models.VIP{
		VIP:      "192.168.1.226",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, movedForReuse)
	require.NoError(t, err)

	oldReusedAfterMoveVIP := reusedAfterMove.VIP
	oldMovedForReuseVIP := movedForReuse.VIP
	txReuseAfterMove, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	movedForReuse.Port = 81
	err = txReuseAfterMove.UpdateVIP(ctx, movedForReuse)
	require.NoError(t, err)
	reusedAfterMove.VIP = oldMovedForReuseVIP
	reusedAfterMove.Port = 80
	err = txReuseAfterMove.UpdateVIP(ctx, reusedAfterMove)
	require.NoError(t, err)
	require.NoError(t, txReuseAfterMove.Commit())

	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      oldReusedAfterMoveVIP,
		Port:     reusedAfterMove.Port,
		Protocol: reusedAfterMove.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.NoError(t, err)
	err = ds.CreateVIP(ctx, &models.VIP{
		VIP:      oldMovedForReuseVIP,
		Port:     81,
		Protocol: movedForReuse.Protocol,
		LBMethod: models.LBMethodMaglev,
	})
	require.ErrorIs(t, err, datastore.ErrConflict)
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

func testWatchDoesNotReplayExistingChanges(t *testing.T, factory DataStoreFactory) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ds, cleanup := factory(ctx, t)
	defer cleanup()

	vip := &models.VIP{
		VIP:      "192.168.1.510",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	events, err := ds.Watch(ctx)
	require.NoError(t, err)

	select {
	case event := <-events:
		t.Fatalf("watch replayed existing event: %v", event.Type)
	case <-time.After(300 * time.Millisecond):
	}

	vip.Port = 443
	err = ds.UpdateVIP(ctx, vip)
	require.NoError(t, err)

	select {
	case event := <-events:
		assert.NotNil(t, event)
		assert.Equal(t, datastore.EventTypeVIPUpdated, event.Type)
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

	createdVIP, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	require.NotNil(t, createdVIP.HealthCheck)
	assert.NotEmpty(t, createdVIP.HealthCheck.ID)
	assert.Equal(t, vip.ID, createdVIP.HealthCheck.VIPID)
	assert.False(t, createdVIP.HealthCheck.CreatedAt.IsZero())
	assert.False(t, createdVIP.HealthCheck.UpdatedAt.IsZero())

	// Set VIPID in health check
	vip.HealthCheck.VIPID = vip.ID
	vip.HealthCheck.ID = createdVIP.HealthCheck.ID
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
	assert.NotEmpty(t, foundVIP.HealthCheck.ID)
	assert.Equal(t, vip.ID, foundVIP.HealthCheck.VIPID)
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
