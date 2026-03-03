// Package routing provides an abstraction for BGP route announcements.
package routing

import "context"

// Router manages BGP route announcements based on backend health.
// When at least one backend is healthy, the VIP route is announced.
// When all backends are unhealthy, the route is withdrawn to prevent black-holing.
type Router interface {
	// AnnounceVIP announces a route for the given VIP address.
	AnnounceVIP(ctx context.Context, vipAddress string) error

	// WithdrawVIP withdraws the route for the given VIP address.
	WithdrawVIP(ctx context.Context, vipAddress string) error

	// IsAnnounced returns whether the route is currently announced.
	IsAnnounced(vipAddress string) bool

	// Close releases any resources held by the router.
	Close() error
}
