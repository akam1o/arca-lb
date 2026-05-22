package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// AddBackend adds a new backend to etcd
func (ds *EtcdDataStore) AddBackend(ctx context.Context, backend *models.Backend) error {
	if backend == nil {
		return datastore.ErrInvalidInput
	}
	if err := datastore.ValidateBackendForWrite(backend); err != nil {
		return err
	}

	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	// Generate UUID if not set
	if backend.ID == "" {
		backend.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if backend.CreatedAt.IsZero() {
		backend.CreatedAt = now
	}
	backend.UpdatedAt = now

	// Serialize backend to JSON
	data, err := json.Marshal(backend)
	if err != nil {
		return fmt.Errorf("failed to marshal backend: %w", err)
	}
	if err := ds.checkBackendIPAvailable(ctx, backend); err != nil {
		return fmt.Errorf("failed to verify backend IP: %w", err)
	}

	// Store backend and index in a transaction
	key := ds.backendKey(backend.VIPID, backend.ID)
	indexKey := ds.backendIndexKey(backend.ID)
	ipIndexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)

	err = ds.commitWithRevision(
		ctx,
		[]etcdTxnCheck{
			{cmp: clientv3.Compare(clientv3.Version(ds.vipKey(backend.VIPID)), ">", 0), err: datastore.ErrNotFound},
			{cmp: clientv3.Compare(clientv3.Version(key), "=", 0), err: datastore.ErrConflict},
			{cmp: clientv3.Compare(clientv3.Version(indexKey), "=", 0), err: datastore.ErrConflict},
			{cmp: clientv3.Compare(clientv3.Version(ipIndexKey), "=", 0), err: datastore.ErrConflict},
		},
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, backend.VIPID), // Store VIP ID for reverse lookup
		clientv3.OpPut(ipIndexKey, backend.ID),
	)

	if err != nil {
		return fmt.Errorf("failed to put backend to etcd: %w", err)
	}

	return nil
}

func (ds *EtcdDataStore) backendIndexKeysForVIP(ctx context.Context, vipID string) ([]string, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	resp, err := ds.client.Get(ctx, ds.backendIndexPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list backend indexes from etcd: %w", err)
	}

	keys := make([]string, 0)
	for _, kv := range resp.Kvs {
		if string(kv.Value) == vipID {
			keys = append(keys, string(kv.Key))
		}
	}

	return keys, nil
}

func (ds *EtcdDataStore) deleteBackendIndexesForVIP(ctx context.Context, vipID string) error {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	if err := ds.deleteBackendIPIndexesForVIP(ctx, vipID); err != nil {
		return err
	}

	indexKeys, err := ds.backendIndexKeysForVIP(ctx, vipID)
	if err != nil {
		return err
	}

	indexPrefix := ds.backendIndexPrefix()
	for _, indexKey := range indexKeys {
		if !strings.HasPrefix(indexKey, indexPrefix) {
			continue
		}

		backendID := strings.TrimPrefix(indexKey, indexPrefix)
		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Value(indexKey), "=", vipID),
			clientv3.Compare(clientv3.Version(ds.backendKey(vipID, backendID)), "=", 0),
		).Then(
			clientv3.OpDelete(indexKey),
		).Commit()
		if err != nil {
			return fmt.Errorf("failed to delete backend indexes from etcd: %w", err)
		}
		if !txnResp.Succeeded {
			continue
		}
	}

	return nil
}

func (ds *EtcdDataStore) deleteBackendIPIndexesForVIP(ctx context.Context, vipID string) error {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	resp, err := ds.client.Get(ctx, ds.backendIPIndexPrefix(vipID), clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to list backend IP indexes from etcd: %w", err)
	}

	for _, kv := range resp.Kvs {
		indexKey := string(kv.Key)
		backendID := string(kv.Value)
		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Value(indexKey), "=", backendID),
			clientv3.Compare(clientv3.Version(ds.backendKey(vipID, backendID)), "=", 0),
		).Then(
			clientv3.OpDelete(indexKey),
		).Commit()
		if err != nil {
			return fmt.Errorf("failed to delete backend IP indexes from etcd: %w", err)
		}
		if !txnResp.Succeeded {
			continue
		}
	}

	return nil
}

// GetBackend retrieves a backend by ID from etcd using the index
func (ds *EtcdDataStore) GetBackend(ctx context.Context, id string) (*models.Backend, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	// Use index to find VIP ID (O(1) lookup)
	indexKey := ds.backendIndexKey(id)
	indexResp, err := ds.client.Get(ctx, indexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get backend index from etcd: %w", err)
	}

	if len(indexResp.Kvs) == 0 {
		return nil, datastore.ErrNotFound
	}

	// Get VIP ID from index
	vipID := string(indexResp.Kvs[0].Value)

	// Retrieve backend data
	key := ds.backendKey(vipID, id)
	resp, err := ds.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get backend from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, datastore.ErrNotFound
	}

	var backend models.Backend
	if err := json.Unmarshal(resp.Kvs[0].Value, &backend); err != nil {
		return nil, fmt.Errorf("failed to unmarshal backend: %w", err)
	}

	return &backend, nil
}

// ListBackends retrieves all backends for a VIP from etcd
func (ds *EtcdDataStore) ListBackends(ctx context.Context, vipID string) ([]models.Backend, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	prefix := ds.backendPrefix(vipID)
	resp, err := ds.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list backends from etcd: %w", err)
	}

	backends := make([]models.Backend, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var backend models.Backend
		if err := json.Unmarshal(kv.Value, &backend); err != nil {
			return nil, fmt.Errorf("failed to unmarshal backend: %w", err)
		}
		backends = append(backends, backend)
	}

	return backends, nil
}

// UpdateBackend updates an existing backend in etcd
func (ds *EtcdDataStore) UpdateBackend(ctx context.Context, backend *models.Backend) error {
	if backend == nil {
		return datastore.ErrInvalidInput
	}

	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	if backend.ID == "" {
		return datastore.ErrInvalidInput
	}
	if err := datastore.ValidateBackendFieldsForWrite(backend); err != nil {
		return err
	}

	// Check if backend exists
	existing, err := ds.GetBackend(ctx, backend.ID)
	if err != nil {
		return fmt.Errorf("backend not found: %w", err)
	}
	oldIPIndexOwned, err := ds.claimBackendIPIndex(ctx, existing)
	if err != nil {
		return fmt.Errorf("failed to ensure backend IP index: %w", err)
	}

	// Preserve CreatedAt and VIPID
	backend.CreatedAt = existing.CreatedAt
	backend.VIPID = existing.VIPID
	backend.UpdatedAt = time.Now()
	if err := datastore.ValidateBackendForWrite(backend); err != nil {
		return err
	}

	// Serialize backend to JSON
	data, err := json.Marshal(backend)
	if err != nil {
		return fmt.Errorf("failed to marshal backend: %w", err)
	}
	if err := ds.checkBackendIPAvailable(ctx, backend); err != nil {
		return fmt.Errorf("failed to verify backend IP: %w", err)
	}

	// Update in etcd only if the backend, reverse index, and parent VIP still exist.
	key := ds.backendKey(backend.VIPID, backend.ID)
	indexKey := ds.backendIndexKey(backend.ID)
	oldIPIndexKey := ds.backendIPIndexKey(existing.VIPID, existing.IP)
	newIPIndexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)
	checks := []etcdTxnCheck{
		{cmp: clientv3.Compare(clientv3.Version(ds.vipKey(backend.VIPID)), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Version(key), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Version(indexKey), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Value(indexKey), "=", backend.VIPID), err: datastore.ErrNotFound},
	}
	if oldIPIndexOwned {
		checks = append(checks,
			etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(oldIPIndexKey), ">", 0),
				err: datastore.ErrNotFound,
			},
			etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Value(oldIPIndexKey), "=", backend.ID),
				err: datastore.ErrNotFound,
			},
		)
	}
	ops := []clientv3.Op{
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, backend.VIPID),
	}
	if oldIPIndexKey != newIPIndexKey {
		checks = append(checks, etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(newIPIndexKey), "=", 0),
			err: datastore.ErrConflict,
		})
		if oldIPIndexOwned {
			ops = append(ops, clientv3.OpDelete(oldIPIndexKey))
		}
		ops = append(ops, clientv3.OpPut(newIPIndexKey, backend.ID))
	} else {
		ops = append(ops, clientv3.OpPut(newIPIndexKey, backend.ID))
	}
	err = ds.commitWithRevision(
		ctx,
		checks,
		ops...,
	)
	if err != nil {
		return fmt.Errorf("failed to update backend in etcd: %w", err)
	}

	return nil
}

// DeleteBackend deletes a backend and its index from etcd
func (ds *EtcdDataStore) DeleteBackend(ctx context.Context, id string) error {
	if id == "" {
		return datastore.ErrInvalidInput
	}

	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	// Get backend to find its VIP ID
	backend, err := ds.GetBackend(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get backend: %w", err)
	}
	ipIndexOwned, err := ds.claimBackendIPIndex(ctx, backend)
	if err != nil {
		return fmt.Errorf("failed to ensure backend IP index: %w", err)
	}

	// Delete backend and index in a transaction
	key := ds.backendKey(backend.VIPID, backend.ID)
	indexKey := ds.backendIndexKey(backend.ID)
	ipIndexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)

	checks := []etcdTxnCheck{
		{cmp: clientv3.Compare(clientv3.Version(key), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Version(indexKey), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Value(indexKey), "=", backend.VIPID), err: datastore.ErrNotFound},
	}
	if ipIndexOwned {
		checks = append(checks,
			etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(ipIndexKey), ">", 0),
				err: datastore.ErrNotFound,
			},
			etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Value(ipIndexKey), "=", backend.ID),
				err: datastore.ErrNotFound,
			},
		)
	}
	ops := []clientv3.Op{
		clientv3.OpDelete(key),
		clientv3.OpDelete(indexKey),
	}
	if ipIndexOwned {
		ops = append(ops, clientv3.OpDelete(ipIndexKey))
	}

	err = ds.commitWithRevision(
		ctx,
		checks,
		ops...,
	)

	if err != nil {
		return fmt.Errorf("failed to delete backend from etcd: %w", err)
	}

	return nil
}
