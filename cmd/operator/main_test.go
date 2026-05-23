package main

import (
	"flag"
	"io"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestBindOperatorFlagsDefaultsToProductionZap(t *testing.T) {
	var opts operatorOptions
	var zapOpts zap.Options
	fs := flag.NewFlagSet("operator", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindOperatorFlags(fs, &opts, &zapOpts)

	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse default flags: %v", err)
	}

	if zapOpts.Development {
		t.Fatal("expected zap development mode to be disabled by default")
	}
	if opts.metricsAddr != ":8080" {
		t.Fatalf("metricsAddr = %q, want :8080", opts.metricsAddr)
	}
	if opts.probeAddr != ":8081" {
		t.Fatalf("probeAddr = %q, want :8081", opts.probeAddr)
	}
	if opts.enableWebhooks {
		t.Fatal("expected webhooks to be disabled by default")
	}
	if opts.enableLeaderElection {
		t.Fatal("expected leader election to be disabled by default")
	}
}

func TestBindOperatorFlagsAcceptsZapAndOperatorOptions(t *testing.T) {
	var opts operatorOptions
	var zapOpts zap.Options
	fs := flag.NewFlagSet("operator", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindOperatorFlags(fs, &opts, &zapOpts)

	err := fs.Parse([]string{
		"--metrics-bind-address=:9090",
		"--health-probe-bind-address=:9091",
		"--enable-webhooks=true",
		"--leader-elect=true",
		"--agent-status-ttl=30s",
		"--agent-status-prune-interval=10s",
		"--zap-devel=true",
		"--zap-encoder=console",
		"--zap-log-level=debug",
		"--zap-stacktrace-level=error",
		"--zap-time-encoding=rfc3339",
	})
	if err != nil {
		t.Fatalf("parse configured flags: %v", err)
	}

	if opts.metricsAddr != ":9090" {
		t.Fatalf("metricsAddr = %q, want :9090", opts.metricsAddr)
	}
	if opts.probeAddr != ":9091" {
		t.Fatalf("probeAddr = %q, want :9091", opts.probeAddr)
	}
	if !opts.enableWebhooks {
		t.Fatal("expected webhooks to be enabled")
	}
	if !opts.enableLeaderElection {
		t.Fatal("expected leader election to be enabled")
	}
	if opts.agentStatusTTL != 30*time.Second {
		t.Fatalf("agentStatusTTL = %s, want 30s", opts.agentStatusTTL)
	}
	if opts.agentStatusPruneInterval != 10*time.Second {
		t.Fatalf("agentStatusPruneInterval = %s, want 10s", opts.agentStatusPruneInterval)
	}
	if !zapOpts.Development {
		t.Fatal("expected zap development mode to be enabled")
	}
	if zapOpts.NewEncoder == nil {
		t.Fatal("expected zap encoder option to be configured")
	}
	if zapOpts.Level == nil {
		t.Fatal("expected zap log level option to be configured")
	}
	if zapOpts.StacktraceLevel == nil {
		t.Fatal("expected zap stacktrace level option to be configured")
	}
	if zapOpts.TimeEncoder == nil {
		t.Fatal("expected zap time encoding option to be configured")
	}
}
