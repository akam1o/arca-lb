package dataplane

import (
	"context"
	"log/slog"
	"sync"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

// Noop implements DataPlane as an in-memory no-op, suitable for tests
// and development environments without VPP.
type Noop struct {
	mu     sync.RWMutex
	vips   map[string]*noopVIPEntry
	logger *slog.Logger
}

type noopVIPEntry struct {
	vip      *v1alpha1.VirtualIP
	backends map[string]v1alpha1.BackendSpec
}

// NewNoop creates a new no-op DataPlane.
func NewNoop() *Noop {
	return &Noop{
		vips:   make(map[string]*noopVIPEntry),
		logger: slog.Default().With("component", "dataplane-noop"),
	}
}

func noopKey(vip *v1alpha1.VirtualIP) string {
	return vip.Namespace + "/" + vip.Name
}

func (n *Noop) ApplyVIP(_ context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := noopKey(vip)
	entry := &noopVIPEntry{
		vip:      vip.DeepCopy(),
		backends: make(map[string]v1alpha1.BackendSpec),
	}
	for _, be := range healthyBackends {
		entry.backends[be.Address] = be
	}
	n.vips[key] = entry
	n.logger.Info("ApplyVIP", "key", key, "backends", len(healthyBackends))
	return nil
}

func (n *Noop) RemoveVIP(_ context.Context, vip *v1alpha1.VirtualIP) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := noopKey(vip)
	delete(n.vips, key)
	n.logger.Info("RemoveVIP", "key", key)
	return nil
}

func (n *Noop) SetBackends(_ context.Context, vip *v1alpha1.VirtualIP, backends []v1alpha1.BackendSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := noopKey(vip)
	entry, ok := n.vips[key]
	if !ok {
		return nil
	}
	entry.backends = make(map[string]v1alpha1.BackendSpec)
	for _, be := range backends {
		entry.backends[be.Address] = be
	}
	n.logger.Info("SetBackends", "key", key, "count", len(backends))
	return nil
}

func (n *Noop) AddBackend(_ context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := noopKey(vip)
	entry, ok := n.vips[key]
	if !ok {
		return nil
	}
	entry.backends[backend.Address] = backend
	n.logger.Info("AddBackend", "key", key, "backend", backend.Address)
	return nil
}

func (n *Noop) RemoveBackend(_ context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := noopKey(vip)
	entry, ok := n.vips[key]
	if !ok {
		return nil
	}
	delete(entry.backends, backend.Address)
	n.logger.Info("RemoveBackend", "key", key, "backend", backend.Address)
	return nil
}

func (n *Noop) GetState(_ context.Context) (*State, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var vips []VIPState
	for _, entry := range n.vips {
		vs := VIPState{
			Name:     entry.vip.Name,
			Address:  entry.vip.Spec.Address,
			Port:     entry.vip.Spec.Port,
			Protocol: entry.vip.Spec.Protocol,
		}
		for _, be := range entry.backends {
			vs.Backends = append(vs.Backends, BackendEntry{Address: be.Address, Weight: be.Weight})
		}
		vips = append(vips, vs)
	}
	return &State{VIPs: vips}, nil
}

func (n *Noop) Close() error { return nil }
