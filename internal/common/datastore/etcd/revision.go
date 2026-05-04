package etcd

import (
	"context"
	"fmt"
	"strconv"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type etcdTxnCheck struct {
	cmp clientv3.Cmp
	err error
}

// initRevision initializes the revision counter in etcd if it doesn't exist
func (ds *EtcdDataStore) initRevision(ctx context.Context) error {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	key := ds.revisionKey()

	_, err := ds.client.Txn(ctx).If(
		clientv3.Compare(clientv3.Version(key), "=", 0),
	).Then(
		clientv3.OpPut(key, "1"),
	).Commit()
	if err != nil {
		return fmt.Errorf("failed to initialize revision in etcd: %w", err)
	}

	return nil
}

// GetRevision retrieves the current revision number
func (ds *EtcdDataStore) GetRevision(ctx context.Context) (int64, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

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
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

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

func (ds *EtcdDataStore) commitWithRevision(ctx context.Context, checks []etcdTxnCheck, ops ...clientv3.Op) error {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	key := ds.revisionKey()

	for {
		resp, err := ds.client.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get revision from etcd: %w", err)
		}
		if len(resp.Kvs) == 0 {
			return fmt.Errorf("revision not found")
		}

		currentValue := string(resp.Kvs[0].Value)
		currentRevision, err := strconv.ParseInt(currentValue, 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse revision: %w", err)
		}

		newValue := strconv.FormatInt(currentRevision+1, 10)

		cmps := make([]clientv3.Cmp, 0, len(checks)+1)
		cmps = append(cmps, clientv3.Compare(clientv3.Value(key), "=", currentValue))
		for _, check := range checks {
			cmps = append(cmps, check.cmp)
		}

		txnOps := make([]clientv3.Op, 0, len(ops)+1)
		txnOps = append(txnOps, ops...)
		txnOps = append(txnOps, clientv3.OpPut(key, newValue))

		txnResp, err := ds.client.Txn(ctx).If(cmps...).Then(txnOps...).Commit()
		if err != nil {
			return fmt.Errorf("failed to commit revision transaction: %w", err)
		}
		if txnResp.Succeeded {
			return nil
		}

		failedCheck, err := ds.firstFailedEtcdTxnCheck(ctx, checks)
		if err != nil {
			return err
		}
		if failedCheck != nil {
			return failedCheck.err
		}
	}
}

func (ds *EtcdDataStore) firstFailedEtcdTxnCheck(ctx context.Context, checks []etcdTxnCheck) (*etcdTxnCheck, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	for i := range checks {
		txnResp, err := ds.client.Txn(ctx).If(checks[i].cmp).Then(clientv3.OpGet(ds.revisionKey())).Commit()
		if err != nil {
			return nil, fmt.Errorf("failed to check transaction condition: %w", err)
		}
		if !txnResp.Succeeded {
			return &checks[i], nil
		}
	}

	return nil, nil
}
