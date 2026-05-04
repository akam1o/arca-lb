package routing

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoopRouter(t *testing.T) {
	r := NewNoop()
	defer func() { _ = r.Close() }()

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
	defer func() { _ = r.Close() }()

	// Should not error
	if err := r.WithdrawVIP(context.Background(), "10.0.0.1"); err != nil {
		t.Fatalf("WithdrawVIP for non-existent: %v", err)
	}
}

func TestFRRRouteCommands(t *testing.T) {
	f := &FRR{config: FRRConfig{RouteTag: 10000}}

	addIPv4, err := f.addRouteCmd("203.0.113.10")
	if err != nil {
		t.Fatalf("addRouteCmd IPv4: %v", err)
	}
	wantAddIPv4 := []string{
		"configure terminal",
		"ip route 203.0.113.10/32 Null0 tag 10000",
		"end",
	}
	if strings.Join(addIPv4, "\n") != strings.Join(wantAddIPv4, "\n") {
		t.Fatalf("addRouteCmd IPv4 = %#v, want %#v", addIPv4, wantAddIPv4)
	}

	addIPv6, err := f.addRouteCmd("2001:db8::10")
	if err != nil {
		t.Fatalf("addRouteCmd IPv6: %v", err)
	}
	wantAddIPv6 := []string{
		"configure terminal",
		"ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"end",
	}
	if strings.Join(addIPv6, "\n") != strings.Join(wantAddIPv6, "\n") {
		t.Fatalf("addRouteCmd IPv6 = %#v, want %#v", addIPv6, wantAddIPv6)
	}

	delIPv6, err := f.deleteRouteCmd("2001:db8::10")
	if err != nil {
		t.Fatalf("deleteRouteCmd IPv6: %v", err)
	}
	wantDelIPv6 := []string{
		"configure terminal",
		"no ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"end",
	}
	if strings.Join(delIPv6, "\n") != strings.Join(wantDelIPv6, "\n") {
		t.Fatalf("deleteRouteCmd IPv6 = %#v, want %#v", delIPv6, wantDelIPv6)
	}

	if _, err := f.addRouteCmd("not-an-ip"); err == nil {
		t.Fatal("expected invalid VIP address to be rejected")
	}
}

func TestFRRExecVTYShUsesMultipleCArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	vtyshPath := filepath.Join(dir, "vtysh")

	quotedArgsFile := "'" + strings.ReplaceAll(argsFile, "'", "'\\''") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + quotedArgsFile + "\n"
	if err := os.WriteFile(vtyshPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake vtysh: %v", err)
	}

	f := &FRR{
		config: FRRConfig{
			VTYShPath:  vtyshPath,
			CmdTimeout: 10 * time.Second,
		},
	}

	commands := []string{
		"configure terminal",
		"ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"end",
	}
	if err := f.execVTYSh(context.Background(), commands); err != nil {
		t.Fatalf("execVTYSh: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}

	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"-c",
		"configure terminal",
		"-c",
		"ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"-c",
		"end",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("vtysh args = %#v, want %#v", got, want)
	}
}

func TestFRRWithdrawUntrackedVIPExecutesDelete(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	vtyshPath := filepath.Join(dir, "vtysh")

	quotedArgsFile := "'" + strings.ReplaceAll(argsFile, "'", "'\\''") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + quotedArgsFile + "\n"
	if err := os.WriteFile(vtyshPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake vtysh: %v", err)
	}

	f := &FRR{
		config: FRRConfig{
			VTYShPath:  vtyshPath,
			RouteTag:   10000,
			CmdTimeout: 10 * time.Second,
		},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		announced: make(map[string]bool),
	}

	if err := f.WithdrawVIP(context.Background(), "2001:db8::10"); err != nil {
		t.Fatalf("WithdrawVIP: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}

	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"-c",
		"configure terminal",
		"-c",
		"no ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"-c",
		"end",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("vtysh args = %#v, want %#v", got, want)
	}
	if f.IsAnnounced("2001:db8::10") {
		t.Fatal("expected route to remain unannounced locally")
	}
}

func TestFRRAnnounceTrackedVIPReplaysAdd(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	vtyshPath := filepath.Join(dir, "vtysh")

	quotedArgsFile := "'" + strings.ReplaceAll(argsFile, "'", "'\\''") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + quotedArgsFile + "\n"
	if err := os.WriteFile(vtyshPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake vtysh: %v", err)
	}

	f := &FRR{
		config: FRRConfig{
			VTYShPath:  vtyshPath,
			RouteTag:   10000,
			CmdTimeout: 10 * time.Second,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		announced: map[string]bool{
			"2001:db8::10": true,
		},
	}

	if err := f.AnnounceVIP(context.Background(), "2001:db8::10"); err != nil {
		t.Fatalf("AnnounceVIP: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}

	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"-c",
		"configure terminal",
		"-c",
		"ipv6 route 2001:db8::10/128 Null0 tag 10000",
		"-c",
		"end",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("vtysh args = %#v, want %#v", got, want)
	}
	if !f.IsAnnounced("2001:db8::10") {
		t.Fatal("expected route to remain announced locally")
	}
}
