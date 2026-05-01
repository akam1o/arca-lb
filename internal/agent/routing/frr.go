package routing

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// FRRConfig holds FRR-specific configuration.
type FRRConfig struct {
	VTYShPath  string
	RouteTag   int
	CmdTimeout time.Duration
}

// FRR implements Router using FRR static routes and vtysh.
type FRR struct {
	config    FRRConfig
	logger    *slog.Logger
	mu        sync.RWMutex
	announced map[string]bool // vipAddress -> announced
}

// NewFRR creates a new FRR router.
func NewFRR(cfg FRRConfig) (*FRR, error) {
	if cfg.VTYShPath == "" {
		cfg.VTYShPath = "/usr/bin/vtysh"
	}
	if cfg.RouteTag == 0 {
		cfg.RouteTag = 10000
	}
	if cfg.CmdTimeout == 0 {
		cfg.CmdTimeout = 10 * time.Second
	}

	if _, err := os.Stat(cfg.VTYShPath); err != nil {
		return nil, fmt.Errorf("vtysh not found at %s: %w", cfg.VTYShPath, err)
	}

	return &FRR{
		config:    cfg,
		logger:    slog.Default().With("component", "routing-frr"),
		announced: make(map[string]bool),
	}, nil
}

func (f *FRR) AnnounceVIP(ctx context.Context, vipAddress string) error {
	f.mu.Lock()
	if f.announced[vipAddress] {
		f.mu.Unlock()
		return nil // already announced
	}
	f.mu.Unlock()

	commands, err := f.addRouteCmd(vipAddress)
	if err != nil {
		return err
	}
	if err := f.execVTYSh(ctx, commands); err != nil {
		return fmt.Errorf("failed to announce route for %s: %w", vipAddress, err)
	}

	f.mu.Lock()
	f.announced[vipAddress] = true
	f.mu.Unlock()

	f.logger.Info("route announced", "vip", vipAddress)
	return nil
}

func (f *FRR) WithdrawVIP(ctx context.Context, vipAddress string) error {
	f.mu.RLock()
	announced := f.announced[vipAddress]
	f.mu.RUnlock()
	if !announced {
		// The agent intentionally leaves FRR routes in place across process
		// restarts, so an empty local map does not prove the route is absent.
		f.logger.Debug("route is not tracked locally, attempting withdraw", "vip", vipAddress)
	}

	commands, err := f.deleteRouteCmd(vipAddress)
	if err != nil {
		return err
	}
	if err := f.execVTYSh(ctx, commands); err != nil {
		return fmt.Errorf("failed to withdraw route for %s: %w", vipAddress, err)
	}

	f.mu.Lock()
	delete(f.announced, vipAddress)
	f.mu.Unlock()

	f.logger.Info("route withdrawn", "vip", vipAddress)
	return nil
}

func (f *FRR) IsAnnounced(vipAddress string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.announced[vipAddress]
}

func (f *FRR) Close() error {
	return nil
}

func (f *FRR) addRouteCmd(addr string) ([]string, error) {
	routeCmd, err := f.routeCmd("ip route", "ipv6 route", addr)
	if err != nil {
		return nil, err
	}
	return []string{"configure terminal", routeCmd, "end"}, nil
}

func (f *FRR) deleteRouteCmd(addr string) ([]string, error) {
	routeCmd, err := f.routeCmd("no ip route", "no ipv6 route", addr)
	if err != nil {
		return nil, err
	}
	return []string{"configure terminal", routeCmd, "end"}, nil
}

func (f *FRR) routeCmd(ipv4Cmd, ipv6Cmd, addr string) (string, error) {
	ip := net.ParseIP(addr)
	if ip == nil {
		return "", fmt.Errorf("invalid VIP address: %s", addr)
	}

	if ip.To4() == nil {
		return fmt.Sprintf("%s %s/128 Null0 tag %d", ipv6Cmd, addr, f.config.RouteTag), nil
	}
	return fmt.Sprintf("%s %s/32 Null0 tag %d", ipv4Cmd, addr, f.config.RouteTag), nil
}

func (f *FRR) execVTYSh(ctx context.Context, commands []string) error {
	ctx, cancel := context.WithTimeout(ctx, f.config.CmdTimeout)
	defer cancel()

	args := make([]string, 0, len(commands)*2)
	for _, command := range commands {
		args = append(args, "-c", command)
	}

	cmd := exec.CommandContext(ctx, f.config.VTYShPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("vtysh error: %w, output: %s", err, string(output))
	}
	return nil
}
