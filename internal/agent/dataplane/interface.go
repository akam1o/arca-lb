// Package dataplane defines the abstraction layer for data-plane backends.
// Implementations include VPP (production), IPVS (lightweight), and Noop (testing).
package dataplane

import (
	"context"
	"fmt"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

// BackendEntry represents a single backend applied to the data plane.
type BackendEntry struct {
	Address string
	Weight  int
}

// VIPState represents the state of a VIP in the data plane.
type VIPState struct {
	Name     string
	Address  string
	Port     int
	Protocol v1alpha1.Protocol
	Backends []BackendEntry
}

// State holds the complete data-plane state snapshot.
type State struct {
	VIPs []VIPState
}

// DataPlane is the abstraction for data-plane operations.
// Implementations must be safe for concurrent use.
type DataPlane interface {
	// ApplyVIP creates or updates a VIP in the data plane.
	// Backends listed in the spec are the full desired set; the implementation
	// performs the necessary diff internally.
	ApplyVIP(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error

	// RemoveVIP removes a VIP and all its backends from the data plane.
	RemoveVIP(ctx context.Context, vip *v1alpha1.VirtualIP) error

	// SetBackends replaces the backend set for a VIP with only the healthy backends.
	SetBackends(ctx context.Context, vip *v1alpha1.VirtualIP, backends []v1alpha1.BackendSpec) error

	// AddBackend adds a single backend to an existing VIP.
	AddBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error

	// RemoveBackend removes a single backend from an existing VIP.
	RemoveBackend(ctx context.Context, vip *v1alpha1.VirtualIP, backend v1alpha1.BackendSpec) error

	// GetState returns a snapshot of the current data-plane state.
	GetState(ctx context.Context) (*State, error)

	// Close releases resources held by the data plane.
	Close() error
}

// New creates a DataPlane from a type name and config.
func New(dpType string, cfg map[string]interface{}) (DataPlane, error) {
	switch dpType {
	case "vpp":
		return NewVPP(cfg)
	case "noop":
		return NewNoop(), nil
	default:
		return nil, fmt.Errorf("unsupported dataplane type: %s", dpType)
	}
}
