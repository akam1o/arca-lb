package rollout

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestLeaseNameIsStableAndDNSLabelSized(t *testing.T) {
	name := LeaseName("virtualip/default/web")
	if name != LeaseName("virtualip/default/web") {
		t.Fatal("LeaseName should be stable for the same key")
	}
	if len(name) > 63 {
		t.Fatalf("lease name length = %d, want <= 63", len(name))
	}
}
