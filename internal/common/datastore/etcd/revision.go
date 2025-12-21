package etcd

import (
	"context"
	"fmt"
	"strconv"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// initRevision initializes the revision counter in etcd if it doesn't exist
func (ds *EtcdDataStore) initRevision(ctx context.Context) error {
	key := ds.revisionKey()
	resp, err := ds.client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get revision from etcd: %w", err)
	}

	// If revision doesn't exist, initialize it to 1
	if len(resp.Kvs) == 0 {
		_, err = ds.client.Put(ctx, key, "1")
		if err != nil {
			return fmt.Errorf("failed to initialize revision in etcd: %w", err)
		}
	}

	return nil
}

// GetRevision retrieves the current revision number
func (ds *EtcdDataStore) GetRevision(ctx context.Context) (int64, error) {
	key := ds.revisionKey()
	resp, err := ds.client.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("failed to get revision from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return 0, fmt.Errorf("revision not found")
	}

	revision, err := strconv.ParseInt(string(resp.Kvs[0].Value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse revision: %w", err)
	}

	return revision, nil
}

// IncrementRevision atomically increments the revision number using Compare-and-Swap
func (ds *EtcdDataStore) IncrementRevision(ctx context.Context) (int64, error) {
	key := ds.revisionKey()

	for {
		// Get current revision
		resp, err := ds.client.Get(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("failed to get revision from etcd: %w", err)
		}

		if len(resp.Kvs) == 0 {
			return 0, fmt.Errorf("revision not found")
		}

		currentValue := string(resp.Kvs[0].Value)
		currentRevision, err := strconv.ParseInt(currentValue, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse revision: %w", err)
		}

		// Calculate new revision
		newRevision := currentRevision + 1
		newValue := strconv.FormatInt(newRevision, 10)

		// Attempt to update with Compare-and-Swap
		txn := ds.client.Txn(ctx)
		txnResp, err := txn.If(
			clientv3.Compare(clientv3.Value(key), "=", currentValue),
		).Then(
			clientv3.OpPut(key, newValue),
		).Commit()

		if err != nil {
			return 0, fmt.Errorf("failed to increment revision: %w", err)
		}

		// If transaction succeeded, return new revision
		if txnResp.Succeeded {
			return newRevision, nil
		}

		// If transaction failed, retry (CAS conflict)
		// The loop will continue and try again with the updated value
	}
}
