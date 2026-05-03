package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// AddBackend adds a new backend to etcd
func (ds *EtcdDataStore) AddBackend(ctx context.Context, backend *models.Backend) error {
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

	// Store backend and index in a transaction
	key := ds.backendKey(backend.VIPID, backend.ID)
	indexKey := ds.backendIndexKey(backend.ID)

	txn := ds.client.Txn(ctx)
	txnResp, err := txn.If(
		clientv3.Compare(clientv3.Version(ds.vipKey(backend.VIPID)), ">", 0),
	).Then(
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, backend.VIPID), // Store VIP ID for reverse lookup
	).Commit()

	if err != nil {
		return fmt.Errorf("failed to put backend to etcd: %w", err)
	}
	if !txnResp.Succeeded {
		return datastore.ErrNotFound
	}

	// Increment revision
	if _, err := ds.IncrementRevision(ctx); err != nil {
		return fmt.Errorf("failed to increment revision: %w", err)
	}

	return nil
}

func (ds *EtcdDataStore) backendIndexKeysForVIP(ctx context.Context, vipID string) ([]string, error) {
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
	indexKeys, err := ds.backendIndexKeysForVIP(ctx, vipID)
	if err != nil {
		return err
	}
	if len(indexKeys) == 0 {
		return nil
	}

	ops := make([]clientv3.Op, 0, len(indexKeys))
	for _, key := range indexKeys {
		ops = append(ops, clientv3.OpDelete(key))
	}

	if _, err := ds.client.Txn(ctx).Then(ops...).Commit(); err != nil {
		return fmt.Errorf("failed to delete backend indexes from etcd: %w", err)
	}

	return nil
}

// GetBackend retrieves a backend by ID from etcd using the index
func (ds *EtcdDataStore) GetBackend(ctx context.Context, id string) (*models.Backend, error) {
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
	if backend.ID == "" {
		return fmt.Errorf("backend ID is required")
	}

	// Check if backend exists
	existing, err := ds.GetBackend(ctx, backend.ID)
	if err != nil {
		return fmt.Errorf("backend not found: %w", err)
	}

	// Preserve CreatedAt and VIPID
	backend.CreatedAt = existing.CreatedAt
	backend.VIPID = existing.VIPID
	backend.UpdatedAt = time.Now()

	// Serialize backend to JSON
	data, err := json.Marshal(backend)
	if err != nil {
		return fmt.Errorf("failed to marshal backend: %w", err)
	}

	// Update in etcd
	key := ds.backendKey(backend.VIPID, backend.ID)
	_, err = ds.client.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("failed to update backend in etcd: %w", err)
	}

	// Increment revision
	if _, err := ds.IncrementRevision(ctx); err != nil {
		return fmt.Errorf("failed to increment revision: %w", err)
	}

	return nil
}

// DeleteBackend deletes a backend and its index from etcd
func (ds *EtcdDataStore) DeleteBackend(ctx context.Context, id string) error {
	// Get backend to find its VIP ID
	backend, err := ds.GetBackend(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get backend: %w", err)
	}

	// Delete backend and index in a transaction
	key := ds.backendKey(backend.VIPID, backend.ID)
	indexKey := ds.backendIndexKey(backend.ID)

	txn := ds.client.Txn(ctx)
	_, err = txn.Then(
		clientv3.OpDelete(key),
		clientv3.OpDelete(indexKey),
	).Commit()

	if err != nil {
		return fmt.Errorf("failed to delete backend from etcd: %w", err)
	}

	// Increment revision
	if _, err := ds.IncrementRevision(ctx); err != nil {
		return fmt.Errorf("failed to increment revision: %w", err)
	}

	return nil
}
