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

// EtcdTransaction implements datastore.Transaction using etcd STM
type EtcdTransaction struct {
	ds                 *EtcdDataStore
	ctx                context.Context
	ops                []clientv3.Op
	checks             []etcdTxnCheck
	createdVIPIDs      map[string]struct{}
	createdBackendIDs  map[string]struct{}
	vipTupleIndexKeys  map[string]string
	vipOriginalIndexes map[string]string
	vipTupleOwners     map[string]string
	backendIPOwners    map[string]string
	deletedVIPIDs      map[string]struct{}
	committed          bool
}

// BeginTx starts a new transaction
func (ds *EtcdDataStore) BeginTx(ctx context.Context) (datastore.Transaction, error) {
	return &EtcdTransaction{
		ds:                 ds,
		ctx:                ctx,
		ops:                make([]clientv3.Op, 0),
		checks:             make([]etcdTxnCheck, 0),
		createdVIPIDs:      make(map[string]struct{}),
		createdBackendIDs:  make(map[string]struct{}),
		vipTupleIndexKeys:  make(map[string]string),
		vipOriginalIndexes: make(map[string]string),
		vipTupleOwners:     make(map[string]string),
		backendIPOwners:    make(map[string]string),
		deletedVIPIDs:      make(map[string]struct{}),
	}, nil
}

// CreateVIP adds a VIP creation operation to the transaction
func (tx *EtcdTransaction) CreateVIP(ctx context.Context, vip *models.VIP) error {
	// Generate UUID if not set
	if vip.ID == "" {
		vip.ID = uuid.New().String()
	}
	if _, created := tx.createdVIPIDs[vip.ID]; created {
		return datastore.ErrConflict
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
	if err := tx.ds.checkVIPTupleAvailable(ctx, vip); err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Add put operation to transaction
	key := tx.ds.vipKey(vip.ID)
	indexKey := tx.ds.vipTupleIndexKey(vip)
	if owner, reserved := tx.vipTupleOwners[indexKey]; reserved && owner != vip.ID {
		return datastore.ErrConflict
	}
	tx.checks = append(tx.checks, etcdTxnCheck{
		cmp: clientv3.Compare(clientv3.Version(key), "=", 0),
		err: datastore.ErrConflict,
	})
	tx.checks = append(tx.checks, etcdTxnCheck{
		cmp: clientv3.Compare(clientv3.Version(indexKey), "=", 0),
		err: datastore.ErrConflict,
	})
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)), clientv3.OpPut(indexKey, vip.ID))
	tx.createdVIPIDs[vip.ID] = struct{}{}
	tx.vipTupleIndexKeys[vip.ID] = indexKey
	tx.vipTupleOwners[indexKey] = vip.ID

	return nil
}

// AddBackend adds a backend creation operation to the transaction
func (tx *EtcdTransaction) AddBackend(ctx context.Context, backend *models.Backend) error {
	// Generate UUID if not set
	if backend.ID == "" {
		backend.ID = uuid.New().String()
	}
	if _, created := tx.createdBackendIDs[backend.ID]; created {
		return datastore.ErrConflict
	}
	if _, deleted := tx.deletedVIPIDs[backend.VIPID]; deleted {
		return datastore.ErrNotFound
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
	if err := tx.ds.checkBackendIPAvailable(ctx, backend); err != nil {
		return fmt.Errorf("failed to verify backend IP: %w", err)
	}

	if _, created := tx.createdVIPIDs[backend.VIPID]; !created {
		if _, err := tx.ds.GetVIP(ctx, backend.VIPID); err != nil {
			return fmt.Errorf("failed to verify VIP: %w", err)
		}
		tx.checks = append(tx.checks, etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(tx.ds.vipKey(backend.VIPID)), ">", 0),
			err: datastore.ErrNotFound,
		})
	}

	// Add put operations to transaction
	key := tx.ds.backendKey(backend.VIPID, backend.ID)
	indexKey := tx.ds.backendIndexKey(backend.ID)
	ipIndexKey := tx.ds.backendIPIndexKey(backend.VIPID, backend.IP)
	if owner, reserved := tx.backendIPOwners[ipIndexKey]; reserved && owner != backend.ID {
		return datastore.ErrConflict
	}
	tx.checks = append(tx.checks,
		etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(key), "=", 0),
			err: datastore.ErrConflict,
		},
		etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(indexKey), "=", 0),
			err: datastore.ErrConflict,
		},
		etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(ipIndexKey), "=", 0),
			err: datastore.ErrConflict,
		},
	)
	tx.ops = append(
		tx.ops,
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, backend.VIPID),
		clientv3.OpPut(ipIndexKey, backend.ID),
	)
	tx.createdBackendIDs[backend.ID] = struct{}{}
	tx.backendIPOwners[ipIndexKey] = backend.ID

	return nil
}

// UpdateVIP adds a VIP update operation to the transaction
func (tx *EtcdTransaction) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	if vip.ID == "" {
		return fmt.Errorf("VIP ID is required")
	}
	if _, deleted := tx.deletedVIPIDs[vip.ID]; deleted {
		return datastore.ErrNotFound
	}

	key := tx.ds.vipKey(vip.ID)
	now := time.Now()
	currentIndexKey := tx.vipTupleIndexKeys[vip.ID]
	originalIndexKey := tx.vipOriginalIndexes[vip.ID]
	if _, created := tx.createdVIPIDs[vip.ID]; !created {
		existing, err := tx.ds.GetVIP(ctx, vip.ID)
		if err != nil {
			return fmt.Errorf("failed to verify VIP: %w", err)
		}
		vip.CreatedAt = existing.CreatedAt
		if originalIndexKey == "" {
			if err := tx.ds.ensureVIPTupleIndex(ctx, existing); err != nil {
				return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
			}
			originalIndexKey = tx.ds.vipTupleIndexKey(existing)
			tx.vipOriginalIndexes[vip.ID] = originalIndexKey
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(key), ">", 0),
				err: datastore.ErrNotFound,
			})
			tx.checks = append(tx.checks,
				etcdTxnCheck{
					cmp: clientv3.Compare(clientv3.Version(originalIndexKey), ">", 0),
					err: datastore.ErrNotFound,
				},
				etcdTxnCheck{
					cmp: clientv3.Compare(clientv3.Value(originalIndexKey), "=", vip.ID),
					err: datastore.ErrNotFound,
				},
			)
		}
		if currentIndexKey == "" {
			currentIndexKey = originalIndexKey
		}
		prepareHealthCheckForVIP(vip, existing, now)
	} else if vip.CreatedAt.IsZero() {
		vip.CreatedAt = now
		prepareHealthCheckForVIP(vip, nil, now)
	} else {
		prepareHealthCheckForVIP(vip, nil, now)
	}
	vip.UpdatedAt = now

	// Serialize VIP to JSON
	data, err := json.Marshal(vip)
	if err != nil {
		return fmt.Errorf("failed to marshal VIP: %w", err)
	}
	if err := tx.ds.checkVIPTupleAvailable(ctx, vip); err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Add put operation to transaction
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)))
	newIndexKey := tx.ds.vipTupleIndexKey(vip)
	if owner, reserved := tx.vipTupleOwners[newIndexKey]; reserved && owner != vip.ID {
		return datastore.ErrConflict
	}
	if currentIndexKey != newIndexKey {
		if newIndexKey != originalIndexKey {
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(newIndexKey), "=", 0),
				err: datastore.ErrConflict,
			})
		}
		tx.ops = append(tx.ops, clientv3.OpDelete(currentIndexKey), clientv3.OpPut(newIndexKey, vip.ID))
		delete(tx.vipTupleOwners, currentIndexKey)
		tx.vipTupleIndexKeys[vip.ID] = newIndexKey
		tx.vipTupleOwners[newIndexKey] = vip.ID
	} else {
		tx.ops = append(tx.ops, clientv3.OpPut(newIndexKey, vip.ID))
		tx.vipTupleIndexKeys[vip.ID] = newIndexKey
		tx.vipTupleOwners[newIndexKey] = vip.ID
	}

	return nil
}

// DeleteVIP adds a VIP deletion operation to the transaction
func (tx *EtcdTransaction) DeleteVIP(ctx context.Context, id string) error {
	if _, deleted := tx.deletedVIPIDs[id]; deleted {
		return datastore.ErrNotFound
	}

	vipKey := tx.ds.vipKey(id)
	indexKey := tx.vipTupleIndexKeys[id]
	if _, created := tx.createdVIPIDs[id]; !created {
		originalIndexKey := tx.vipOriginalIndexes[id]
		if originalIndexKey == "" {
			vip, err := tx.ds.GetVIP(ctx, id)
			if err != nil {
				if errors.Is(err, datastore.ErrNotFound) {
					if cleanupErr := tx.ds.deleteBackendIndexesForVIP(ctx, id); cleanupErr != nil {
						return cleanupErr
					}
				}
				return fmt.Errorf("failed to verify VIP: %w", err)
			}
			if err := tx.ds.ensureVIPTupleIndex(ctx, vip); err != nil {
				return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
			}
			originalIndexKey = tx.ds.vipTupleIndexKey(vip)
			tx.vipOriginalIndexes[id] = originalIndexKey
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(vipKey), ">", 0),
				err: datastore.ErrNotFound,
			})
			tx.checks = append(tx.checks,
				etcdTxnCheck{
					cmp: clientv3.Compare(clientv3.Version(originalIndexKey), ">", 0),
					err: datastore.ErrNotFound,
				},
				etcdTxnCheck{
					cmp: clientv3.Compare(clientv3.Value(originalIndexKey), "=", id),
					err: datastore.ErrNotFound,
				},
			)
		}
		if indexKey == "" {
			indexKey = originalIndexKey
		}
	}
	if indexKey == "" {
		return datastore.ErrNotFound
	}

	// Add delete VIP and associated backends operations. Reverse indexes are
	// cleaned up after commit so large VIPs do not exceed etcd's transaction
	// operation limit.
	backendPrefix := tx.ds.backendPrefix(id)
	tx.ops = append(
		tx.ops,
		clientv3.OpDelete(vipKey),
		clientv3.OpDelete(backendPrefix, clientv3.WithPrefix()),
		clientv3.OpDelete(indexKey),
	)
	delete(tx.vipTupleOwners, indexKey)
	delete(tx.vipTupleIndexKeys, id)
	delete(tx.vipOriginalIndexes, id)
	tx.deletedVIPIDs[id] = struct{}{}

	return nil
}

// Commit commits the transaction
func (tx *EtcdTransaction) Commit() error {
	if len(tx.ops) == 0 {
		return nil
	}

	if !tx.committed {
		if err := tx.ds.commitWithRevision(tx.ctx, tx.checks, tx.ops...); err != nil {
			if errors.Is(err, datastore.ErrNotFound) {
				if cleanupErr := tx.cleanupDeletedVIPIndexes(); cleanupErr != nil {
					return cleanupErr
				}
			}
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx.committed = true
	}

	return tx.cleanupDeletedVIPIndexes()
}

func (tx *EtcdTransaction) cleanupDeletedVIPIndexes() error {
	ctx := tx.ctx
	cancel := func() {}
	if tx.committed && ctx.Err() != nil {
		ctx, cancel = context.WithTimeout(context.Background(), tx.ds.requestTimeout)
	}
	defer cancel()

	for vipID := range tx.deletedVIPIDs {
		if err := tx.ds.deleteBackendIndexesForVIP(ctx, vipID); err != nil {
			return err
		}
		delete(tx.deletedVIPIDs, vipID)
	}

	return nil
}

// Rollback rolls back the transaction (no-op for etcd as operations are not applied until commit)
func (tx *EtcdTransaction) Rollback() error {
	// Clear operations
	tx.ops = nil
	return nil
}
