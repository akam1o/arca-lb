package routing

import (
	"context"
	"log/slog"
	"sync"
)

// Noop implements Router as a no-op for environments without FRR.
type Noop struct {
	mu        sync.RWMutex
	announced map[string]bool
	logger    *slog.Logger
}

// NewNoop creates a no-op router.
func NewNoop() *Noop {
	return &Noop{
		announced: make(map[string]bool),
		logger:    slog.Default().With("component", "routing-noop"),
	}
}

func (n *Noop) AnnounceVIP(_ context.Context, vipAddress string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.announced[vipAddress] = true
	n.logger.Info("route announced (noop)", "vip", vipAddress)
	return nil
}

func (n *Noop) WithdrawVIP(_ context.Context, vipAddress string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.announced, vipAddress)
	n.logger.Info("route withdrawn (noop)", "vip", vipAddress)
	return nil
}

func (n *Noop) IsAnnounced(vipAddress string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.announced[vipAddress]
}

func (n *Noop) Close() error { return nil }
