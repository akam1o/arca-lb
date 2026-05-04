package etcd

import (
	"context"
	"testing"
	"time"
)

func TestContextWithRequestTimeoutAppliesWhenContextHasNoDeadline(t *testing.T) {
	timeout := 50 * time.Millisecond
	ds := &EtcdDataStore{requestTimeout: timeout}
	before := time.Now()

	ctx, cancel := ds.contextWithRequestTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("context deadline is not set")
	}
	if deadline.Before(before.Add(timeout / 2)) {
		t.Fatalf("deadline = %v, want at least %v", deadline, before.Add(timeout/2))
	}
	if deadline.After(before.Add(2 * timeout)) {
		t.Fatalf("deadline = %v, want within request timeout", deadline)
	}
}

func TestContextWithRequestTimeoutPreservesExistingDeadline(t *testing.T) {
	ds := &EtcdDataStore{requestTimeout: 50 * time.Millisecond}
	parentDeadline := time.Now().Add(time.Hour)
	parent, parentCancel := context.WithDeadline(context.Background(), parentDeadline)
	defer parentCancel()

	ctx, cancel := ds.contextWithRequestTimeout(parent)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("context deadline is not set")
	}
	if !deadline.Equal(parentDeadline) {
		t.Fatalf("deadline = %v, want existing deadline %v", deadline, parentDeadline)
	}
}

func TestContextWithRequestTimeoutAllowsDisabledTimeout(t *testing.T) {
	ds := &EtcdDataStore{}

	ctx, cancel := ds.contextWithRequestTimeout(context.Background())
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("deadline should not be set when request timeout is disabled")
	}
}
