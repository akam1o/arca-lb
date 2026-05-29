package rollout

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCoordinatorSerializesRunExclusive(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	c1 := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-a",
		LeaseDuration:  time.Minute,
		RetryInterval:  10 * time.Millisecond,
	}, logger)
	c2 := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-b",
		LeaseDuration:  time.Minute,
		RetryInterval:  10 * time.Millisecond,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c1Started := make(chan struct{})
	releaseC1 := make(chan struct{})
	c1Done := make(chan error, 1)
	go func() {
		c1Done <- c1.RunExclusive(ctx, "virtualip/default/web", func(ctx context.Context) error {
			close(c1Started)
			select {
			case <-releaseC1:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	select {
	case <-c1Started:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first coordinator")
	}

	c2Started := make(chan struct{})
	c2Done := make(chan error, 1)
	go func() {
		c2Done <- c2.RunExclusive(ctx, "virtualip/default/web", func(context.Context) error {
			close(c2Started)
			return nil
		})
	}()

	select {
	case <-c2Started:
		t.Fatal("second coordinator entered before the first released the lease")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseC1)
	if err := <-c1Done; err != nil {
		t.Fatalf("first RunExclusive: %v", err)
	}

	select {
	case <-c2Started:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second coordinator")
	}
	if err := <-c2Done; err != nil {
		t.Fatalf("second RunExclusive: %v", err)
	}
}

func TestCoordinatorSerializesSameProcessRunExclusive(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	c := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-a",
		LeaseDuration:  time.Minute,
		RetryInterval:  10 * time.Millisecond,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- c.RunExclusive(ctx, "vip-address/203.0.113.10", func(ctx context.Context) error {
			close(firstStarted)
			select {
			case <-releaseFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first RunExclusive")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- c.RunExclusive(ctx, "vip-address/203.0.113.10", func(context.Context) error {
			close(secondStarted)
			return nil
		})
	}()

	select {
	case <-secondStarted:
		t.Fatal("second same-process RunExclusive entered before the first released")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first RunExclusive: %v", err)
	}

	select {
	case <-secondStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second RunExclusive")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second RunExclusive: %v", err)
	}
}

func TestCoordinatorSerializesSameConfiguredHolderIdentity(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	c1 := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-a",
		LeaseDuration:  time.Minute,
		RetryInterval:  10 * time.Millisecond,
	}, logger)
	c2 := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-a",
		LeaseDuration:  time.Minute,
		RetryInterval:  10 * time.Millisecond,
	}, logger)
	if c1.holderIdentity == c2.holderIdentity {
		t.Fatal("coordinators with the same configured holder must use distinct lease holders")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- c1.RunExclusive(ctx, "vip-address/203.0.113.10", func(ctx context.Context) error {
			close(firstStarted)
			select {
			case <-releaseFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first coordinator")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- c2.RunExclusive(ctx, "vip-address/203.0.113.10", func(context.Context) error {
			close(secondStarted)
			return nil
		})
	}()

	select {
	case <-secondStarted:
		t.Fatal("coordinator with same configured holder entered before the first released")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first RunExclusive: %v", err)
	}

	select {
	case <-secondStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second coordinator")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second RunExclusive: %v", err)
	}
}

func TestCoordinatorReportsInFlightRenewalErrorAfterFunctionReturns(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	k8sClient := &blockingRenewUpdateClient{
		Client:       fake.NewClientBuilder().WithScheme(scheme).Build(),
		renewStarted: make(chan struct{}),
		allowRenew:   make(chan struct{}),
		renewErr:     errors.New("renew failed"),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	c := NewWithClient(k8sClient, Config{
		Namespace:      "arca-lb-system",
		HolderIdentity: "node-a",
		LeaseDuration:  30 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
		ReleaseTimeout: time.Second,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fnReturned := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.RunExclusive(ctx, "vip-address/203.0.113.10", func(ctx context.Context) error {
			select {
			case <-k8sClient.renewStarted:
			case <-ctx.Done():
				return ctx.Err()
			}
			close(fnReturned)
			return nil
		})
	}()

	select {
	case <-fnReturned:
	case <-ctx.Done():
		t.Fatal("timed out waiting for exclusive function to return")
	}

	time.Sleep(20 * time.Millisecond)
	close(k8sClient.allowRenew)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "renew failed") {
			t.Fatalf("RunExclusive error = %v, want renewal failure", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for RunExclusive")
	}
}

func TestLeaseNameIsStableAndDNSLabelSized(t *testing.T) {
	name := LeaseName("virtualip/default/web")
	if name != LeaseName("virtualip/default/web") {
		t.Fatal("LeaseName should be stable for the same key")
	}
	if len(name) > 63 {
		t.Fatalf("lease name length = %d, want <= 63", len(name))
	}
}

type blockingRenewUpdateClient struct {
	controllerclient.Client

	updateCalls  atomic.Int32
	renewStarted chan struct{}
	allowRenew   chan struct{}
	renewErr     error
}

func (c *blockingRenewUpdateClient) Update(ctx context.Context, obj controllerclient.Object, opts ...controllerclient.UpdateOption) error {
	if _, ok := obj.(*coordinationv1.Lease); ok && c.updateCalls.Add(1) == 1 {
		close(c.renewStarted)
		select {
		case <-c.allowRenew:
			return c.renewErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}
