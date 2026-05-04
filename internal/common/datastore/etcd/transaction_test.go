package etcd

import (
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestCompactEtcdWriteOpsKeepsFinalWritePerKey(t *testing.T) {
	ops := []clientv3.Op{
		clientv3.OpPut("/arca-lb/vips/vip-1", "old"),
		clientv3.OpPut("/arca-lb/vip-index/TCP/80/192.0.2.10", "vip-1"),
		clientv3.OpDelete("/arca-lb/vip-index/TCP/80/192.0.2.10"),
		clientv3.OpPut("/arca-lb/vips/vip-1", "new"),
		clientv3.OpPut("/arca-lb/vip-index/TCP/81/192.0.2.10", "vip-1"),
	}

	got := compactEtcdWriteOps(ops)
	if len(got) != 3 {
		t.Fatalf("len(compactEtcdWriteOps) = %d, want 3", len(got))
	}
	assertOpDelete(t, got[0], "/arca-lb/vip-index/TCP/80/192.0.2.10")
	assertOpPut(t, got[1], "/arca-lb/vips/vip-1", "new")
	assertOpPut(t, got[2], "/arca-lb/vip-index/TCP/81/192.0.2.10", "vip-1")
}

func TestCompactEtcdWriteOpsPreservesLastWritePosition(t *testing.T) {
	ops := []clientv3.Op{
		clientv3.OpPut("/arca-lb/backends/vip-1/backend-1", "stale"),
		clientv3.OpDelete("/arca-lb/backends/vip-1/", clientv3.WithPrefix()),
		clientv3.OpPut("/arca-lb/backends/vip-1/backend-1", "final"),
	}

	got := compactEtcdWriteOps(ops)
	if len(got) != 2 {
		t.Fatalf("len(compactEtcdWriteOps) = %d, want 2", len(got))
	}
	assertOpDelete(t, got[0], "/arca-lb/backends/vip-1/")
	assertOpPut(t, got[1], "/arca-lb/backends/vip-1/backend-1", "final")
}

func assertOpPut(t *testing.T, op clientv3.Op, key, value string) {
	t.Helper()
	if !op.IsPut() {
		t.Fatalf("op for %q is not a put", key)
	}
	if got := string(op.KeyBytes()); got != key {
		t.Fatalf("put key = %q, want %q", got, key)
	}
	if got := string(op.ValueBytes()); got != value {
		t.Fatalf("put value = %q, want %q", got, value)
	}
}

func assertOpDelete(t *testing.T, op clientv3.Op, key string) {
	t.Helper()
	if !op.IsDelete() {
		t.Fatalf("op for %q is not a delete", key)
	}
	if got := string(op.KeyBytes()); got != key {
		t.Fatalf("delete key = %q, want %q", got, key)
	}
}
