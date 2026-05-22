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
	vipOriginalOwned   map[string]bool
	vipReleasedTuples  map[string]string
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
		vipOriginalOwned:   make(map[string]bool),
		vipReleasedTuples:  make(map[string]string),
		vipTupleOwners:     make(map[string]string),
		backendIPOwners:    make(map[string]string),
		deletedVIPIDs:      make(map[string]struct{}),
	}, nil
}

func (tx *EtcdTransaction) checkVIPTupleAvailable(ctx context.Context, vip *models.VIP) (bool, error) {
	indexKey := tx.ds.vipTupleIndexKey(vip)
	needsEmptyIndexCheck := true
	if _, released := tx.vipReleasedTuples[indexKey]; released {
		needsEmptyIndexCheck = false
	}

	if owner, reserved := tx.vipTupleOwners[indexKey]; reserved && owner != vip.ID {
		return false, datastore.ErrConflict
	}

	vips, err := tx.ds.ListVIPs(ctx)
	if err != nil {
		return false, err
	}

	for i := range vips {
		existing := &vips[i]
		if existing.ID == vip.ID || !sameVIPTuple(existing, vip) {
			continue
		}
		if _, deleted := tx.deletedVIPIDs[existing.ID]; deleted {
			needsEmptyIndexCheck = false
			continue
		}
		if currentIndexKey, touched := tx.vipTupleIndexKeys[existing.ID]; touched && currentIndexKey != indexKey {
			needsEmptyIndexCheck = false
			continue
		}
		return false, datastore.ErrConflict
	}

	return needsEmptyIndexCheck, nil
}

func (tx *EtcdTransaction) releaseVIPTuple(ownerID, indexKey string) {
	if indexKey == "" {
		return
	}
	if tx.vipOriginalIndexes[ownerID] == indexKey && tx.vipOriginalOwned[ownerID] {
		tx.vipReleasedTuples[indexKey] = ownerID
	}
	delete(tx.vipTupleOwners, indexKey)
}

type etcdWriteOpKey struct {
	key      string
	rangeEnd string
}

func compactEtcdWriteOps(ops []clientv3.Op) []clientv3.Op {
	lastWriteIndexes := make(map[etcdWriteOpKey]int, len(ops))
	for i, op := range ops {
		if !isEtcdWriteOp(op) {
			continue
		}
		lastWriteIndexes[etcdWriteOpKeyFor(op)] = i
	}
	compacted := make([]clientv3.Op, 0, len(ops))
	for i, op := range ops {
		if !isEtcdWriteOp(op) {
			compacted = append(compacted, op)
			continue
		}
		if isCoveredByLaterRangeDelete(ops, i, op) {
			continue
		}
		if lastWriteIndexes[etcdWriteOpKeyFor(op)] == i {
			compacted = append(compacted, op)
		}
	}

	return compacted
}

func isCoveredByLaterRangeDelete(ops []clientv3.Op, currentIndex int, op clientv3.Op) bool {
	for i := currentIndex + 1; i < len(ops); i++ {
		later := ops[i]
		if !later.IsDelete() || len(later.RangeBytes()) == 0 {
			continue
		}
		if etcdDeleteRangeCoversOp(later, op) {
			return true
		}
	}
	return false
}

func etcdDeleteRangeCoversOp(deleteOp, op clientv3.Op) bool {
	start := string(deleteOp.KeyBytes())
	end := string(deleteOp.RangeBytes())
	key := string(op.KeyBytes())
	rangeEnd := string(op.RangeBytes())

	if rangeEnd == "" {
		return key >= start && key < end
	}
	return key >= start && rangeEnd <= end
}

func isEtcdWriteOp(op clientv3.Op) bool {
	return op.IsPut() || op.IsDelete()
}

func etcdWriteOpKeyFor(op clientv3.Op) etcdWriteOpKey {
	return etcdWriteOpKey{
		key:      string(op.KeyBytes()),
		rangeEnd: string(op.RangeBytes()),
	}
}

// CreateVIP adds a VIP creation operation to the transaction
func (tx *EtcdTransaction) CreateVIP(ctx context.Context, vip *models.VIP) error {
	if vip == nil {
		return datastore.ErrInvalidInput
	}

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
	needsEmptyIndexCheck, err := tx.checkVIPTupleAvailable(ctx, vip)
	if err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Add put operation to transaction
	key := tx.ds.vipKey(vip.ID)
	indexKey := tx.ds.vipTupleIndexKey(vip)
	tx.checks = append(tx.checks, etcdTxnCheck{
		cmp: clientv3.Compare(clientv3.Version(key), "=", 0),
		err: datastore.ErrConflict,
	})
	if needsEmptyIndexCheck {
		tx.checks = append(tx.checks, etcdTxnCheck{
			cmp: clientv3.Compare(clientv3.Version(indexKey), "=", 0),
			err: datastore.ErrConflict,
		})
	}
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)), clientv3.OpPut(indexKey, vip.ID))
	tx.createdVIPIDs[vip.ID] = struct{}{}
	tx.vipTupleIndexKeys[vip.ID] = indexKey
	tx.vipTupleOwners[indexKey] = vip.ID

	return nil
}

// AddBackend adds a backend creation operation to the transaction
func (tx *EtcdTransaction) AddBackend(ctx context.Context, backend *models.Backend) error {
	if backend == nil {
		return datastore.ErrInvalidInput
	}

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
	if vip == nil {
		return datastore.ErrInvalidInput
	}

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
	originalIndexOwned := tx.vipOriginalOwned[vip.ID]
	if _, created := tx.createdVIPIDs[vip.ID]; !created {
		existing, err := tx.ds.GetVIP(ctx, vip.ID)
		if err != nil {
			return fmt.Errorf("failed to verify VIP: %w", err)
		}
		vip.CreatedAt = existing.CreatedAt
		if originalIndexKey == "" {
			owned, err := tx.ds.claimVIPTupleIndex(ctx, existing)
			if err != nil {
				return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
			}
			originalIndexKey = tx.ds.vipTupleIndexKey(existing)
			tx.vipOriginalIndexes[vip.ID] = originalIndexKey
			tx.vipOriginalOwned[vip.ID] = owned
			originalIndexOwned = owned
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(key), ">", 0),
				err: datastore.ErrNotFound,
			})
			if owned {
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
		}
		if currentIndexKey == "" && originalIndexOwned {
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
	needsEmptyIndexCheck, err := tx.checkVIPTupleAvailable(ctx, vip)
	if err != nil {
		return fmt.Errorf("failed to verify VIP tuple: %w", err)
	}

	// Add put operation to transaction
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)))
	newIndexKey := tx.ds.vipTupleIndexKey(vip)
	if currentIndexKey != newIndexKey {
		if (newIndexKey != originalIndexKey || originalIndexOwned) && needsEmptyIndexCheck {
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(newIndexKey), "=", 0),
				err: datastore.ErrConflict,
			})
		}
		if currentIndexKey != "" {
			tx.ops = append(tx.ops, clientv3.OpDelete(currentIndexKey))
			tx.releaseVIPTuple(vip.ID, currentIndexKey)
		}
		tx.ops = append(tx.ops, clientv3.OpPut(newIndexKey, vip.ID))
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
		originalIndexOwned := tx.vipOriginalOwned[id]
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
			owned, err := tx.ds.claimVIPTupleIndex(ctx, vip)
			if err != nil {
				return fmt.Errorf("failed to ensure VIP tuple index: %w", err)
			}
			originalIndexKey = tx.ds.vipTupleIndexKey(vip)
			tx.vipOriginalIndexes[id] = originalIndexKey
			tx.vipOriginalOwned[id] = owned
			originalIndexOwned = owned
			tx.checks = append(tx.checks, etcdTxnCheck{
				cmp: clientv3.Compare(clientv3.Version(vipKey), ">", 0),
				err: datastore.ErrNotFound,
			})
			if owned {
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
		}
		if indexKey == "" && originalIndexOwned {
			indexKey = originalIndexKey
		}
	}
	if indexKey == "" {
		if _, created := tx.createdVIPIDs[id]; created {
			return datastore.ErrNotFound
		}
	}

	// Add delete VIP and associated backends operations. Reverse indexes are
	// cleaned up after commit so large VIPs do not exceed etcd's transaction
	// operation limit.
	backendPrefix := tx.ds.backendPrefix(id)
	tx.ops = append(
		tx.ops,
		clientv3.OpDelete(vipKey),
		clientv3.OpDelete(backendPrefix, clientv3.WithPrefix()),
	)
	if indexKey != "" {
		tx.ops = append(tx.ops, clientv3.OpDelete(indexKey))
		tx.releaseVIPTuple(id, indexKey)
	}
	delete(tx.vipTupleIndexKeys, id)
	delete(tx.vipOriginalIndexes, id)
	delete(tx.vipOriginalOwned, id)
	tx.deletedVIPIDs[id] = struct{}{}

	return nil
}

// Commit commits the transaction
func (tx *EtcdTransaction) Commit() error {
	ops := compactEtcdWriteOps(tx.ops)
	if len(ops) == 0 {
		return nil
	}

	if !tx.committed {
		if err := tx.ds.commitWithRevision(tx.ctx, tx.checks, ops...); err != nil {
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
