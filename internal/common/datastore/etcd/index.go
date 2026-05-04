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

func (ds *EtcdDataStore) ensureVIPTupleIndex(ctx context.Context, vip *models.VIP) error {
	indexKey := ds.vipTupleIndexKey(vip)

	for {
		resp, err := ds.client.Get(ctx, indexKey)
		if err != nil {
			return fmt.Errorf("failed to get VIP tuple index from etcd: %w", err)
		}
		if len(resp.Kvs) > 0 {
			if string(resp.Kvs[0].Value) == vip.ID {
				return nil
			}
			return datastore.ErrConflict
		}

		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Version(ds.vipKey(vip.ID)), ">", 0),
			clientv3.Compare(clientv3.Version(indexKey), "=", 0),
		).Then(
			clientv3.OpPut(indexKey, vip.ID),
		).Commit()
		if err != nil {
			return fmt.Errorf("failed to ensure VIP tuple index in etcd: %w", err)
		}
		if txnResp.Succeeded {
			return nil
		}

		vipResp, err := ds.client.Get(ctx, ds.vipKey(vip.ID))
		if err != nil {
			return fmt.Errorf("failed to verify VIP from etcd: %w", err)
		}
		if len(vipResp.Kvs) == 0 {
			return datastore.ErrNotFound
		}
	}
}

func (ds *EtcdDataStore) ensureBackendIPIndex(ctx context.Context, backend *models.Backend) error {
	indexKey := ds.backendIPIndexKey(backend.VIPID, backend.IP)

	for {
		resp, err := ds.client.Get(ctx, indexKey)
		if err != nil {
			return fmt.Errorf("failed to get backend IP index from etcd: %w", err)
		}
		if len(resp.Kvs) > 0 {
			if string(resp.Kvs[0].Value) == backend.ID {
				return nil
			}
			return datastore.ErrConflict
		}

		txnResp, err := ds.client.Txn(ctx).If(
			clientv3.Compare(clientv3.Version(ds.backendKey(backend.VIPID, backend.ID)), ">", 0),
			clientv3.Compare(clientv3.Version(indexKey), "=", 0),
		).Then(
			clientv3.OpPut(indexKey, backend.ID),
		).Commit()
		if err != nil {
			return fmt.Errorf("failed to ensure backend IP index in etcd: %w", err)
		}
		if txnResp.Succeeded {
			return nil
		}

		backendResp, err := ds.client.Get(ctx, ds.backendKey(backend.VIPID, backend.ID))
		if err != nil {
			return fmt.Errorf("failed to verify backend from etcd: %w", err)
		}
		if len(backendResp.Kvs) == 0 {
			return datastore.ErrNotFound
		}
	}
}
