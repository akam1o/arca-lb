package etcd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// CreateVIP creates a new VIP in etcd
func (ds *EtcdDataStore) CreateVIP(ctx context.Context, vip *models.VIP) error {
	// Generate UUID if not set
	if vip.ID == "" {
		vip.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if vip.CreatedAt.IsZero() {
		vip.CreatedAt = now
	}
	vip.UpdatedAt = now

	// Set default LB method if not specified
	if vip.LBMethod == "" {
		vip.LBMethod = models.LBMethodMaglev
	}
	prepareHealthCheckForVIP(vip, nil, now)

	// Serialize VIP to JSON
	data, err := json.Marshal(vip)
	if err != nil {
		return fmt.Errorf("failed to marshal VIP: %w", err)
	}
	if err := ds.checkVIPTupleAvailable(ctx, vip); err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Store in etcd
	key := ds.vipKey(vip.ID)
	indexKey := ds.vipTupleIndexKey(vip)
	err = ds.commitWithRevision(
		ctx,
		[]etcdTxnCheck{
			{cmp: clientv3.Compare(clientv3.Version(key), "=", 0), err: datastore.ErrConflict},
			{cmp: clientv3.Compare(clientv3.Version(indexKey), "=", 0), err: datastore.ErrConflict},
		},
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, vip.ID),
	)
	if err != nil {
		return fmt.Errorf("failed to put VIP to etcd: %w", err)
	}

	return nil
}

func prepareHealthCheckForVIP(vip, existing *models.VIP, now time.Time) {
	if vip.HealthCheck == nil {
		return
	}

	if vip.HealthCheck.ID == "" {
		if existing != nil && existing.HealthCheck != nil && existing.HealthCheck.ID != "" {
			vip.HealthCheck.ID = existing.HealthCheck.ID
		} else {
			vip.HealthCheck.ID = uuid.New().String()
		}
	}
	vip.HealthCheck.VIPID = vip.ID

	if vip.HealthCheck.CreatedAt.IsZero() {
		if existing != nil && existing.HealthCheck != nil && !existing.HealthCheck.CreatedAt.IsZero() {
			vip.HealthCheck.CreatedAt = existing.HealthCheck.CreatedAt
		} else {
			vip.HealthCheck.CreatedAt = now
		}
	}
	vip.HealthCheck.UpdatedAt = now
}

// GetVIP retrieves a VIP by ID from etcd
func (ds *EtcdDataStore) GetVIP(ctx context.Context, id string) (*models.VIP, error) {
	key := ds.vipKey(id)
	resp, err := ds.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get VIP from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, datastore.ErrNotFound
	}

	var vip models.VIP
	if err := json.Unmarshal(resp.Kvs[0].Value, &vip); err != nil {
		return nil, fmt.Errorf("failed to unmarshal VIP: %w", err)
	}

	return &vip, nil
}

// ListVIPs retrieves all VIPs from etcd
func (ds *EtcdDataStore) ListVIPs(ctx context.Context) ([]models.VIP, error) {
	prefix := ds.vipPrefix()
	resp, err := ds.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list VIPs from etcd: %w", err)
	}

	vips := make([]models.VIP, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var vip models.VIP
		if err := json.Unmarshal(kv.Value, &vip); err != nil {
			return nil, fmt.Errorf("failed to unmarshal VIP: %w", err)
		}
		vips = append(vips, vip)
	}

	return vips, nil
}

// UpdateVIP updates an existing VIP in etcd
func (ds *EtcdDataStore) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	if vip.ID == "" {
		return fmt.Errorf("VIP ID is required")
	}

	// Check if VIP exists
	existing, err := ds.GetVIP(ctx, vip.ID)
	if err != nil {
		return fmt.Errorf("VIP not found: %w", err)
	}
	if err := ds.ensureVIPTupleIndex(ctx, existing); err != nil {
		return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
	}

	// Preserve CreatedAt
	vip.CreatedAt = existing.CreatedAt
	now := time.Now()
	vip.UpdatedAt = now
	prepareHealthCheckForVIP(vip, existing, now)

	// Serialize VIP to JSON
	data, err := json.Marshal(vip)
	if err != nil {
		return fmt.Errorf("failed to marshal VIP: %w", err)
	}
	if err := ds.checkVIPTupleAvailable(ctx, vip); err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Update in etcd only if the VIP still exists.
	key := ds.vipKey(vip.ID)
	oldIndexKey := ds.vipTupleIndexKey(existing)
	newIndexKey := ds.vipTupleIndexKey(vip)
	checks := []etcdTxnCheck{
		{cmp: clientv3.Compare(clientv3.Version(key), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Version(oldIndexKey), ">", 0), err: datastore.ErrNotFound},
		{cmp: clientv3.Compare(clientv3.Value(oldIndexKey), "=", vip.ID), err: datastore.ErrNotFound},
	}
	ops := []clientv3.Op{clientv3.OpPut(key, string(data))}
	if oldIndexKey != newIndexKey {
		checks = append(checks, etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(newIndexKey), "=", 0),
			err: datastore.ErrConflict,
		})
		ops = append(ops, clientv3.OpDelete(oldIndexKey), clientv3.OpPut(newIndexKey, vip.ID))
	} else {
		ops = append(ops, clientv3.OpPut(newIndexKey, vip.ID))
	}
	err = ds.commitWithRevision(
		ctx,
		checks,
		ops...,
	)
	if err != nil {
		return fmt.Errorf("failed to update VIP in etcd: %w", err)
	}

	return nil
}

// DeleteVIP deletes a VIP and its associated backends from etcd
func (ds *EtcdDataStore) DeleteVIP(ctx context.Context, id string) error {
	vip, err := ds.GetVIP(ctx, id)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			if cleanupErr := ds.deleteBackendIndexesForVIP(ctx, id); cleanupErr != nil {
				return cleanupErr
			}
		}
		return fmt.Errorf("failed to delete VIP from etcd: %w", err)
	}
	if err := ds.ensureVIPTupleIndex(ctx, vip); err != nil {
		return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
	}

	vipKey := ds.vipKey(id)
	backendPrefix := ds.backendPrefix(id)
	indexKey := ds.vipTupleIndexKey(vip)

	ops := []clientv3.Op{
		clientv3.OpDelete(vipKey),
		clientv3.OpDelete(backendPrefix, clientv3.WithPrefix()),
		clientv3.OpDelete(indexKey),
	}

	err = ds.commitWithRevision(
		ctx,
		[]etcdTxnCheck{
			{cmp: clientv3.Compare(clientv3.Version(vipKey), ">", 0), err: datastore.ErrNotFound},
			{cmp: clientv3.Compare(clientv3.Version(indexKey), ">", 0), err: datastore.ErrNotFound},
			{cmp: clientv3.Compare(clientv3.Value(indexKey), "=", id), err: datastore.ErrNotFound},
		},
		ops...,
	)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			if cleanupErr := ds.deleteBackendIndexesForVIP(ctx, id); cleanupErr != nil {
				return cleanupErr
			}
		}
		return fmt.Errorf("failed to delete VIP from etcd: %w", err)
	}

	// Clean up indexes that may have been added before the VIP delete committed.
	if err := ds.deleteBackendIndexesForVIP(ctx, id); err != nil {
		return err
	}

	return nil
}
