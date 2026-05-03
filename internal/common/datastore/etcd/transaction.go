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

// EtcdTransaction implements datastore.Transaction using etcd STM
type EtcdTransaction struct {
	ds              *EtcdDataStore
	ctx             context.Context
	ops             []clientv3.Op
	cmps            []clientv3.Cmp
	createdVIPIDs   map[string]struct{}
	deletedVIPIDs   map[string]struct{}
	backendIDsByVIP map[string][]string
}

// BeginTx starts a new transaction
func (ds *EtcdDataStore) BeginTx(ctx context.Context) (datastore.Transaction, error) {
	return &EtcdTransaction{
		ds:              ds,
		ctx:             ctx,
		ops:             make([]clientv3.Op, 0),
		cmps:            make([]clientv3.Cmp, 0),
		createdVIPIDs:   make(map[string]struct{}),
		deletedVIPIDs:   make(map[string]struct{}),
		backendIDsByVIP: make(map[string][]string),
	}, nil
}

// CreateVIP adds a VIP creation operation to the transaction
func (tx *EtcdTransaction) CreateVIP(ctx context.Context, vip *models.VIP) error {
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

	// Serialize VIP to JSON
	data, err := json.Marshal(vip)
	if err != nil {
		return fmt.Errorf("failed to marshal VIP: %w", err)
	}

	// Add put operation to transaction
	key := tx.ds.vipKey(vip.ID)
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)))
	tx.createdVIPIDs[vip.ID] = struct{}{}

	return nil
}

// AddBackend adds a backend creation operation to the transaction
func (tx *EtcdTransaction) AddBackend(ctx context.Context, backend *models.Backend) error {
	// Generate UUID if not set
	if backend.ID == "" {
		backend.ID = uuid.New().String()
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

	if _, created := tx.createdVIPIDs[backend.VIPID]; !created {
		if _, err := tx.ds.GetVIP(ctx, backend.VIPID); err != nil {
			return fmt.Errorf("failed to verify VIP: %w", err)
		}
		tx.cmps = append(tx.cmps, clientv3.Compare(clientv3.Version(tx.ds.vipKey(backend.VIPID)), ">", 0))
	}

	// Add put operations to transaction
	key := tx.ds.backendKey(backend.VIPID, backend.ID)
	indexKey := tx.ds.backendIndexKey(backend.ID)
	tx.ops = append(
		tx.ops,
		clientv3.OpPut(key, string(data)),
		clientv3.OpPut(indexKey, backend.VIPID),
	)
	tx.backendIDsByVIP[backend.VIPID] = append(tx.backendIDsByVIP[backend.VIPID], backend.ID)

	return nil
}

// UpdateVIP adds a VIP update operation to the transaction
func (tx *EtcdTransaction) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	if vip.ID == "" {
		return fmt.Errorf("VIP ID is required")
	}

	vip.UpdatedAt = time.Now()

	// Serialize VIP to JSON
	data, err := json.Marshal(vip)
	if err != nil {
		return fmt.Errorf("failed to marshal VIP: %w", err)
	}

	// Add put operation to transaction
	key := tx.ds.vipKey(vip.ID)
	tx.ops = append(tx.ops, clientv3.OpPut(key, string(data)))

	return nil
}

// DeleteVIP adds a VIP deletion operation to the transaction
func (tx *EtcdTransaction) DeleteVIP(ctx context.Context, id string) error {
	vipKey := tx.ds.vipKey(id)
	if _, created := tx.createdVIPIDs[id]; !created {
		if _, err := tx.ds.GetVIP(ctx, id); err != nil {
			return fmt.Errorf("failed to verify VIP: %w", err)
		}
		tx.cmps = append(tx.cmps, clientv3.Compare(clientv3.Version(vipKey), ">", 0))
	}

	indexKeys, err := tx.ds.backendIndexKeysForVIP(ctx, id)
	if err != nil {
		return err
	}

	// Add delete VIP, associated backends, and reverse indexes operations.
	backendPrefix := tx.ds.backendPrefix(id)
	tx.ops = append(
		tx.ops,
		clientv3.OpDelete(vipKey),
		clientv3.OpDelete(backendPrefix, clientv3.WithPrefix()),
	)
	for _, indexKey := range indexKeys {
		tx.ops = append(tx.ops, clientv3.OpDelete(indexKey))
	}
	for _, backendID := range tx.backendIDsByVIP[id] {
		tx.ops = append(tx.ops, clientv3.OpDelete(tx.ds.backendIndexKey(backendID)))
	}
	tx.deletedVIPIDs[id] = struct{}{}

	return nil
}

// Commit commits the transaction
func (tx *EtcdTransaction) Commit() error {
	if len(tx.ops) == 0 {
		return nil
	}

	// Execute all operations in a transaction
	txn := tx.ds.client.Txn(tx.ctx)
	if len(tx.cmps) > 0 {
		txn = txn.If(tx.cmps...)
	}
	txnResp, err := txn.Then(tx.ops...).Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	if !txnResp.Succeeded {
		return datastore.ErrNotFound
	}

	for vipID := range tx.deletedVIPIDs {
		if err := tx.ds.deleteBackendIndexesForVIP(tx.ctx, vipID); err != nil {
			return err
		}
	}

	// Increment revision after successful commit
	if _, err := tx.ds.IncrementRevision(tx.ctx); err != nil {
		return fmt.Errorf("failed to increment revision: %w", err)
	}

	return nil
}

// Rollback rolls back the transaction (no-op for etcd as operations are not applied until commit)
func (tx *EtcdTransaction) Rollback() error {
	// Clear operations
	tx.ops = nil
	return nil
}
