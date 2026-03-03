package routing

import (
	"context"
	"testing"
)

func TestNoopRouter(t *testing.T) {
	r := NewNoop()
	defer r.Close()

	ctx := context.Background()

	// Should not error
	if err := r.AnnounceVIP(ctx, "203.0.113.1"); err != nil {
		t.Fatalf("AnnounceVIP: %v", err)
	}

	if !r.IsAnnounced("203.0.113.1") {
		t.Error("expected 203.0.113.1 to be announced")
	}

	if r.IsAnnounced("10.0.0.1") {
		t.Error("expected 10.0.0.1 to not be announced")
	}

	if err := r.WithdrawVIP(ctx, "203.0.113.1"); err != nil {
		t.Fatalf("WithdrawVIP: %v", err)
	}

	if r.IsAnnounced("203.0.113.1") {
		t.Error("expected 203.0.113.1 to not be announced after withdraw")
	}
}

func TestNoopWithdrawNonexistent(t *testing.T) {
	r := NewNoop()
	defer r.Close()

	// Should not error
	if err := r.WithdrawVIP(context.Background(), "10.0.0.1"); err != nil {
		t.Fatalf("WithdrawVIP for non-existent: %v", err)
	}
}
