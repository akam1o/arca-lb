//go:build integration

package etcd

import (
	"context"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/datastore/testsuite"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEtcdDataStore_CommonTests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	factory := func(ctx context.Context, t *testing.T) (datastore.DataStore, func()) {
		cfg := &datastore.Config{
			Type:          "etcd",
			EtcdEndpoints: []string{"http://localhost:2379"},
			EtcdKeyPrefix: "/arca-lb-test",
		}

		ds, err := NewEtcdDataStore(ctx, cfg)
		require.NoError(t, err)

		cleanup := func() {
			// Clean up test data
			// Note: In a real scenario, you might want to delete all keys with the test prefix
			ds.Close()
		}

		return ds, cleanup
	}

	testsuite.RunDataStoreTests(t, factory)
}

func TestEtcdDataStore_Concurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-concurrent",
	}

	ds, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	defer ds.Close()

	// Create VIP
	vip := &models.VIP{
		VIP:      "192.168.1.700",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// Concurrent updates
	done := make(chan bool, 2)

	go func() {
		vip.Port = 443
		ds.UpdateVIP(ctx, vip)
		done <- true
	}()

	go func() {
		vip.LBMethod = models.LBMethodRoundRobin
		ds.UpdateVIP(ctx, vip)
		done <- true
	}()

	<-done
	<-done

	// Verify final state
	retrieved, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	// Last write wins
	assert.True(t, retrieved.Port == 443 || retrieved.LBMethod == models.LBMethodRoundRobin)
}

func TestEtcdDataStore_Idempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-idempotent",
	}

	ds, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	defer ds.Close()

	// Delete non-existent VIP should not error (idempotent)
	err = ds.DeleteVIP(ctx, "non-existent-vip-id")
	assert.NoError(t, err)

	// Create and delete VIP twice
	vip := &models.VIP{
		VIP:      "192.168.1.800",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	err = ds.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	// Delete again should not error
	err = ds.DeleteVIP(ctx, vip.ID)
	assert.NoError(t, err)
}

