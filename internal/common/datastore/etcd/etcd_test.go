//go:build integration

package etcd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/datastore/testsuite"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
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

		dsIface, err := NewEtcdDataStore(ctx, cfg)
		require.NoError(t, err)
		ds := dsIface.(*EtcdDataStore)
		_, err = ds.client.Delete(ctx, ds.keyPrefix, clientv3.WithPrefix())
		require.NoError(t, err)
		require.NoError(t, ds.initRevision(ctx))

		cleanup := func() {
			_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
			_ = ds.Close()
		}

		return ds, cleanup
	}

	testsuite.RunDataStoreTests(t, factory)
}

func newEtcdDataStoreForTest(t *testing.T, ctx context.Context, prefix string) *EtcdDataStore {
	t.Helper()

	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: prefix,
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	t.Cleanup(func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	})

	return ds
}

func putRawVIP(t *testing.T, ctx context.Context, ds *EtcdDataStore, vip *models.VIP) {
	t.Helper()

	data, err := json.Marshal(vip)
	require.NoError(t, err)
	_, err = ds.client.Put(ctx, ds.vipKey(vip.ID), string(data))
	require.NoError(t, err)
}

func putRawBackend(t *testing.T, ctx context.Context, ds *EtcdDataStore, backend *models.Backend) {
	t.Helper()

	data, err := json.Marshal(backend)
	require.NoError(t, err)
	_, err = ds.client.Put(ctx, ds.backendKey(backend.VIPID, backend.ID), string(data))
	require.NoError(t, err)
	_, err = ds.client.Put(ctx, ds.backendIndexKey(backend.ID), backend.VIPID)
	require.NoError(t, err)
}

func requireEtcdValue(t *testing.T, ctx context.Context, ds *EtcdDataStore, key, want string) {
	t.Helper()

	resp, err := ds.client.Get(ctx, key)
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)
	assert.Equal(t, want, string(resp.Kvs[0].Value))
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
		vip.Protocol = models.ProtocolUDP
		ds.UpdateVIP(ctx, vip)
		done <- true
	}()

	<-done
	<-done

	// Verify final state
	retrieved, err := ds.GetVIP(ctx, vip.ID)
	require.NoError(t, err)
	// Last write wins
	assert.True(t, retrieved.Port == 443 || retrieved.Protocol == models.ProtocolUDP)
}

func TestEtcdDataStore_DeleteVIPMissingReturnsNotFound(t *testing.T) {
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

	err = ds.DeleteVIP(ctx, "non-existent-vip-id")
	assert.ErrorIs(t, err, datastore.ErrNotFound)

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

	err = ds.DeleteVIP(ctx, vip.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
}

func TestEtcdDataStore_DeleteVIPDeletesBackendIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-delete-backend-index",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	vip := &models.VIP{
		VIP:      "192.168.1.900",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.9",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, backend)
	require.NoError(t, err)

	indexKey := ds.backendIndexKey(backend.ID)
	resp, err := ds.client.Get(ctx, indexKey)
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)

	ipIndexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)
	resp, err = ds.client.Get(ctx, ipIndexKey)
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)

	err = ds.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	resp, err = ds.client.Get(ctx, indexKey)
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)

	resp, err = ds.client.Get(ctx, ipIndexKey)
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)

	_, err = ds.GetBackend(ctx, backend.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
}

func TestEtcdDataStore_UpdateVIPCanResolveLegacyDuplicateTuple(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	ds := newEtcdDataStoreForTest(t, ctx, "/arca-lb-test-update-legacy-duplicate-vip")

	owner := &models.VIP{
		ID:       "legacy-vip-owner",
		VIP:      "192.168.1.910",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	duplicate := &models.VIP{
		ID:       "legacy-vip-duplicate",
		VIP:      owner.VIP,
		Port:     owner.Port,
		Protocol: owner.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	putRawVIP(t, ctx, ds, owner)
	putRawVIP(t, ctx, ds, duplicate)
	_, err := ds.client.Put(ctx, ds.vipTupleIndexKey(owner), owner.ID)
	require.NoError(t, err)

	duplicate.VIP = "192.168.1.911"
	err = ds.UpdateVIP(ctx, duplicate)
	require.NoError(t, err)

	requireEtcdValue(t, ctx, ds, ds.vipTupleIndexKey(owner), owner.ID)
	requireEtcdValue(t, ctx, ds, ds.vipTupleIndexKey(duplicate), duplicate.ID)
}

func TestEtcdDataStore_DeleteVIPCanRemoveLegacyDuplicateTuple(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	ds := newEtcdDataStoreForTest(t, ctx, "/arca-lb-test-delete-legacy-duplicate-vip")

	owner := &models.VIP{
		ID:       "legacy-vip-owner",
		VIP:      "192.168.1.912",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	duplicate := &models.VIP{
		ID:       "legacy-vip-duplicate",
		VIP:      owner.VIP,
		Port:     owner.Port,
		Protocol: owner.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	putRawVIP(t, ctx, ds, owner)
	putRawVIP(t, ctx, ds, duplicate)
	_, err := ds.client.Put(ctx, ds.vipTupleIndexKey(owner), owner.ID)
	require.NoError(t, err)

	err = ds.DeleteVIP(ctx, duplicate.ID)
	require.NoError(t, err)

	_, err = ds.GetVIP(ctx, duplicate.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
	requireEtcdValue(t, ctx, ds, ds.vipTupleIndexKey(owner), owner.ID)
}

func TestEtcdTransaction_UpdateVIPCanResolveLegacyDuplicateTuple(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	ds := newEtcdDataStoreForTest(t, ctx, "/arca-lb-test-tx-update-legacy-duplicate-vip")

	owner := &models.VIP{
		ID:       "legacy-vip-owner",
		VIP:      "192.168.1.913",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	duplicate := &models.VIP{
		ID:       "legacy-vip-duplicate",
		VIP:      owner.VIP,
		Port:     owner.Port,
		Protocol: owner.Protocol,
		LBMethod: models.LBMethodMaglev,
	}
	putRawVIP(t, ctx, ds, owner)
	putRawVIP(t, ctx, ds, duplicate)
	_, err := ds.client.Put(ctx, ds.vipTupleIndexKey(owner), owner.ID)
	require.NoError(t, err)

	tx, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	duplicate.VIP = "192.168.1.914"
	err = tx.UpdateVIP(ctx, duplicate)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	requireEtcdValue(t, ctx, ds, ds.vipTupleIndexKey(owner), owner.ID)
	requireEtcdValue(t, ctx, ds, ds.vipTupleIndexKey(duplicate), duplicate.ID)
}

func TestEtcdDataStore_UpdateBackendCanResolveLegacyDuplicateIP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	ds := newEtcdDataStoreForTest(t, ctx, "/arca-lb-test-update-legacy-duplicate-backend")

	vip := &models.VIP{
		VIP:      "192.168.1.915",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	owner := &models.Backend{
		ID:     "legacy-backend-owner",
		VIPID:  vip.ID,
		IP:     "10.0.0.20",
		Weight: 10,
	}
	duplicate := &models.Backend{
		ID:     "legacy-backend-duplicate",
		VIPID:  vip.ID,
		IP:     owner.IP,
		Weight: 10,
	}
	putRawBackend(t, ctx, ds, owner)
	putRawBackend(t, ctx, ds, duplicate)
	_, err = ds.client.Put(ctx, ds.backendIPIndexKey(owner.VIPID, owner.IP), owner.ID)
	require.NoError(t, err)

	duplicate.IP = "10.0.0.21"
	err = ds.UpdateBackend(ctx, duplicate)
	require.NoError(t, err)

	requireEtcdValue(t, ctx, ds, ds.backendIPIndexKey(owner.VIPID, owner.IP), owner.ID)
	requireEtcdValue(t, ctx, ds, ds.backendIPIndexKey(duplicate.VIPID, duplicate.IP), duplicate.ID)
}

func TestEtcdDataStore_DeleteBackendCanRemoveLegacyDuplicateIP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	ds := newEtcdDataStoreForTest(t, ctx, "/arca-lb-test-delete-legacy-duplicate-backend")

	vip := &models.VIP{
		VIP:      "192.168.1.916",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err := ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	owner := &models.Backend{
		ID:     "legacy-backend-owner",
		VIPID:  vip.ID,
		IP:     "10.0.0.22",
		Weight: 10,
	}
	duplicate := &models.Backend{
		ID:     "legacy-backend-duplicate",
		VIPID:  vip.ID,
		IP:     owner.IP,
		Weight: 10,
	}
	putRawBackend(t, ctx, ds, owner)
	putRawBackend(t, ctx, ds, duplicate)
	_, err = ds.client.Put(ctx, ds.backendIPIndexKey(owner.VIPID, owner.IP), owner.ID)
	require.NoError(t, err)

	err = ds.DeleteBackend(ctx, duplicate.ID)
	require.NoError(t, err)

	_, err = ds.GetBackend(ctx, duplicate.ID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)
	requireEtcdValue(t, ctx, ds, ds.backendIPIndexKey(owner.VIPID, owner.IP), owner.ID)
}

func TestEtcdDataStore_DeleteVIPMissingCleansStaleBackendIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-delete-missing-cleans-index",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	vipID := "missing-vip-with-stale-index"
	backendID := "stale-backend"
	indexKey := ds.backendIndexKey(backendID)
	_, err = ds.client.Put(ctx, indexKey, vipID)
	require.NoError(t, err)

	err = ds.DeleteVIP(ctx, vipID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)

	resp, err := ds.client.Get(ctx, indexKey)
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)
}

func TestEtcdTransaction_DeleteVIPMissingCleansStaleBackendIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-tx-delete-missing-cleans-index",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	vipID := "missing-tx-vip-with-stale-index"
	backendID := "stale-tx-backend"
	indexKey := ds.backendIndexKey(backendID)
	_, err = ds.client.Put(ctx, indexKey, vipID)
	require.NoError(t, err)

	tx, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	err = tx.DeleteVIP(ctx, vipID)
	assert.ErrorIs(t, err, datastore.ErrNotFound)

	resp, err := ds.client.Get(ctx, indexKey)
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)
}

func TestEtcdTransaction_CommitRetryAfterCleanupFailureCleansStaleBackendIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-tx-commit-retry-cleans-index",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	vip := &models.VIP{
		VIP:      "192.168.1.902",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.11",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, backend)
	require.NoError(t, err)

	txIface, err := ds.BeginTx(ctx)
	require.NoError(t, err)
	tx := txIface.(*EtcdTransaction)
	err = tx.DeleteVIP(ctx, vip.ID)
	require.NoError(t, err)

	err = tx.ds.commitWithRevision(tx.ctx, tx.checks, tx.ops...)
	require.NoError(t, err)
	tx.committed = true

	indexKey := ds.backendIndexKey(backend.ID)
	resp, err := ds.client.Get(ctx, indexKey)
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)

	cancel()

	err = tx.Commit()
	require.NoError(t, err)

	resp, err = ds.client.Get(context.Background(), indexKey)
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)
}

func TestEtcdDataStore_BackendIndexCleanupPreservesLiveBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-cleanup-preserves-live-index",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	vip := &models.VIP{
		VIP:      "192.168.1.901",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}
	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	backend := &models.Backend{
		VIPID:  vip.ID,
		IP:     "10.0.0.10",
		Weight: 10,
	}
	err = ds.AddBackend(ctx, backend)
	require.NoError(t, err)

	err = ds.deleteBackendIndexesForVIP(ctx, vip.ID)
	require.NoError(t, err)

	retrieved, err := ds.GetBackend(ctx, backend.ID)
	require.NoError(t, err)
	assert.Equal(t, backend.ID, retrieved.ID)

	ipIndexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)
	resp, err := ds.client.Get(ctx, ipIndexKey)
	require.NoError(t, err)
	assert.Len(t, resp.Kvs, 1)
}

func TestEtcdDataStore_InitRevisionDoesNotOverwriteExistingRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg := &datastore.Config{
		Type:          "etcd",
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdKeyPrefix: "/arca-lb-test-init-revision-preserves-existing",
	}

	dsIface, err := NewEtcdDataStore(ctx, cfg)
	require.NoError(t, err)
	ds := dsIface.(*EtcdDataStore)
	defer func() {
		_, _ = ds.client.Delete(context.Background(), ds.keyPrefix, clientv3.WithPrefix())
		_ = ds.Close()
	}()

	_, err = ds.client.Put(ctx, ds.revisionKey(), "42")
	require.NoError(t, err)

	err = ds.initRevision(ctx)
	require.NoError(t, err)

	revision, err := ds.GetRevision(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(42), revision)
}
