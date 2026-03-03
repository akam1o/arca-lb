package routing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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

	cmd := f.addRouteCmd(vipAddress)
	if err := f.execVTYSh(ctx, cmd); err != nil {
		return fmt.Errorf("failed to announce route for %s: %w", vipAddress, err)
	}

	f.mu.Lock()
	f.announced[vipAddress] = true
	f.mu.Unlock()

	f.logger.Info("route announced", "vip", vipAddress)
	return nil
}

func (f *FRR) WithdrawVIP(ctx context.Context, vipAddress string) error {
	f.mu.Lock()
	if !f.announced[vipAddress] {
		f.mu.Unlock()
		return nil // not announced
	}
	f.mu.Unlock()

	cmd := f.deleteRouteCmd(vipAddress)
	if err := f.execVTYSh(ctx, cmd); err != nil {
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

func (f *FRR) addRouteCmd(addr string) string {
	prefix := addr + "/32"
	if strings.Contains(addr, ":") {
		prefix = addr + "/128"
	}
	return fmt.Sprintf(
		"configure terminal\nip route %s Null0 tag %d\nend",
		prefix, f.config.RouteTag,
	)
}

func (f *FRR) deleteRouteCmd(addr string) string {
	prefix := addr + "/32"
	if strings.Contains(addr, ":") {
		prefix = addr + "/128"
	}
	return fmt.Sprintf(
		"configure terminal\nno ip route %s Null0 tag %d\nend",
		prefix, f.config.RouteTag,
	)
}

func (f *FRR) execVTYSh(ctx context.Context, commands string) error {
	ctx, cancel := context.WithTimeout(ctx, f.config.CmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, f.config.VTYShPath, "-c", commands)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("vtysh error: %w, output: %s", err, string(output))
	}
	return nil
}
