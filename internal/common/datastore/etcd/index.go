package etcd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func etcdKeyEscape(value string) string {
	return url.PathEscape(value)
}

func (ds *EtcdDataStore) vipTupleIndexKey(vip *models.VIP) string {
	return fmt.Sprintf(
		"%s/vip-index/%s/%d/%s",
		ds.keyPrefix,
		vip.Protocol,
		vip.Port,
		etcdKeyEscape(vip.VIP),
	)
}

func sameVIPTuple(a, b *models.VIP) bool {
	return a.VIP == b.VIP && a.Port == b.Port && a.Protocol == b.Protocol
}

func (ds *EtcdDataStore) backendIPIndexKey(vipID, ip string) string {
	return fmt.Sprintf("%s/backend-ip-index/%s/%s", ds.keyPrefix, vipID, etcdKeyEscape(ip))
}

func (ds *EtcdDataStore) backendIPIndexPrefix(vipID string) string {
	return fmt.Sprintf("%s/backend-ip-index/%s/", ds.keyPrefix, vipID)
}

func (ds *EtcdDataStore) checkVIPTupleAvailable(ctx context.Context, vip *models.VIP) error {
	vips, err := ds.ListVIPs(ctx)
	if err != nil {
		return err
	}

	for i := range vips {
		existing := &vips[i]
		if existing.ID != vip.ID && sameVIPTuple(existing, vip) {
			return datastore.ErrConflict
		}
	}

	return nil
}

func (ds *EtcdDataStore) checkBackendIPAvailable(ctx context.Context, backend *models.Backend) error {
	backends, err := ds.ListBackends(ctx, backend.VIPID)
	if err != nil {
		return err
	}

	for i := range backends {
		existing := &backends[i]
		if existing.ID != backend.ID && existing.IP == backend.IP {
			return datastore.ErrConflict
		}
	}

	return nil
}

func (ds *EtcdDataStore) getIndexOwner(ctx context.Context, indexKey, description string) (string, bool, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	resp, err := ds.client.Get(ctx, indexKey)
	if err != nil {
		return "", false, fmt.Errorf("failed to get %s from etcd: %w", description, err)
	}
	if len(resp.Kvs) == 0 {
		return "", false, nil
	}

	return string(resp.Kvs[0].Value), true, nil
}

func (ds *EtcdDataStore) claimVIPTupleIndex(ctx context.Context, vip *models.VIP) (bool, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	indexKey := ds.vipTupleIndexKey(vip)

	for {
		owner, exists, err := ds.getIndexOwner(ctx, indexKey, "VIP tuple index")
		if err != nil {
			return false, err
		}
		if exists {
			return owner == vip.ID, nil
		}

		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Version(ds.vipKey(vip.ID)), ">", 0),
			clientv3.Compare(clientv3.Version(indexKey), "=", 0),
		).Then(
			clientv3.OpPut(indexKey, vip.ID),
		).Commit()
		if err != nil {
			return false, fmt.Errorf("failed to ensure VIP tuple index in etcd: %w", err)
		}
		if txnResp.Succeeded {
			return true, nil
		}

		vipResp, err := ds.client.Get(ctx, ds.vipKey(vip.ID))
		if err != nil {
			return false, fmt.Errorf("failed to verify VIP from etcd: %w", err)
		}
		if len(vipResp.Kvs) == 0 {
			return false, datastore.ErrNotFound
		}
	}
}

func (ds *EtcdDataStore) claimBackendIPIndex(ctx context.Context, backend *models.Backend) (bool, error) {
	ctx, cancel := ds.contextWithRequestTimeout(ctx)
	defer cancel()

	indexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)

	for {
		owner, exists, err := ds.getIndexOwner(ctx, indexKey, "backend IP index")
		if err != nil {
			return false, err
		}
		if exists {
			return owner == backend.ID, nil
		}

		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Version(ds.backendKey(backend.VIPID, backend.ID)), ">", 0),
			clientv3.Compare(clientv3.Version(indexKey), "=", 0),
		).Then(
			clientv3.OpPut(indexKey, backend.ID),
		).Commit()
		if err != nil {
			return false, fmt.Errorf("failed to ensure backend IP index in etcd: %w", err)
		}
		if txnResp.Succeeded {
			return true, nil
		}

		backendResp, err := ds.client.Get(ctx, ds.backendKey(backend.VIPID, backend.ID))
		if err != nil {
			return false, fmt.Errorf("failed to verify backend from etcd: %w", err)
		}
		if len(backendResp.Kvs) == 0 {
			return false, datastore.ErrNotFound
		}
	}
}
