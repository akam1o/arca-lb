// Package dataplane defines the abstraction layer for data-plane backends.
// Implementations include VPP (production) and Noop (testing).
package dataplane

import (
	"context"
	"errors"
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

// VIPTuningDrift describes a retained dataplane VIP that is forwarding-compatible
// with the desired VIP but has a tuning-only setting that should be repaired.
type VIPTuningDrift struct {
	Field   string
	Current string
	Desired string
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

// TuningDriftReporter is optionally implemented by data planes that can detect
// tuning drift while adopting retained dataplane state after an agent restart.
type TuningDriftReporter interface {
	TuningDrifts(vipKey string) []VIPTuningDrift
}

// VIPRecreator is optionally implemented by data planes that can recreate a VIP
// after the reconciler has drained traffic from the local node.
type VIPRecreator interface {
	RecreateVIP(ctx context.Context, vip *v1alpha1.VirtualIP, healthyBackends []v1alpha1.BackendSpec) error
}

// VIPRecreateStage identifies the phase that failed during a VIP recreate.
type VIPRecreateStage string

const (
	VIPRecreateStageDelete   VIPRecreateStage = "delete"
	VIPRecreateStageAdd      VIPRecreateStage = "add"
	VIPRecreateStageBackends VIPRecreateStage = "backends"
)

// VIPRecreateError annotates recreate failures with whether the old VIP is
// still forwarding traffic and the address route can be safely restored.
type VIPRecreateError struct {
	Stage              VIPRecreateStage
	RouteSafeToRestore bool
	Err                error
}

func (e *VIPRecreateError) Error() string {
	if e == nil || e.Err == nil {
		return "VIP recreate failed"
	}
	if e.Stage == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("VIP recreate %s failed: %v", e.Stage, e.Err)
}

func (e *VIPRecreateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RouteSafeToRestoreAfterVIPRecreate reports whether a recreate failure left
// the previous VIP confirmed in place, allowing the caller to re-advertise it.
func RouteSafeToRestoreAfterVIPRecreate(err error) bool {
	var recreateErr *VIPRecreateError
	return errors.As(err, &recreateErr) && recreateErr.RouteSafeToRestore
}

// VIPUpdateDrainChecker is optionally implemented by data planes that need
// route drain coordination before applying some in-place VIP updates.
type VIPUpdateDrainChecker interface {
	NeedsDrainForVIPUpdate(current, desired *v1alpha1.VirtualIP) (bool, error)
}

// RetainedVIPDrainChecker is optionally implemented by data planes that can
// detect retained VIPs whose forwarding attributes need a route drain before
// they can be safely replaced.
type RetainedVIPDrainChecker interface {
	NeedsDrainForRetainedVIP(ctx context.Context, vip *v1alpha1.VirtualIP) (bool, error)
}

// Config holds typed configuration for data-plane implementations.
type Config struct {
	VPP VPPConfig
}

// New creates a DataPlane from a type name and config.
func New(dpType string, cfg Config) (DataPlane, error) {
	switch dpType {
	case "vpp":
		return NewVPP(cfg.VPP)
	case "noop":
		return NewNoop(), nil
	default:
		return nil, fmt.Errorf("unsupported dataplane type: %s", dpType)
	}
}
