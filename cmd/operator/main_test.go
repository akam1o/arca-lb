package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"testing"
	"time"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
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
	if opts.metricsAddr != "127.0.0.1:8080" {
		t.Fatalf("metricsAddr = %q, want 127.0.0.1:8080", opts.metricsAddr)
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

func TestRunOperatorReturnsErrorWhenConfigUnavailable(t *testing.T) {
	restoreArgs := setOperatorArgs(t, "operator")
	defer restoreArgs()

	fs := flag.NewFlagSet("operator", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	code := runOperator(fs, operatorRuntime{
		getConfig: func() (*rest.Config, error) {
			return nil, errors.New("kubeconfig unavailable")
		},
		newManager: func(*rest.Config, ctrl.Options) (ctrl.Manager, error) {
			t.Fatal("newManager should not be called when config lookup fails")
			return nil, nil
		},
	})

	if code != 1 {
		t.Fatalf("runOperator exit code = %d, want 1", code)
	}
}

func TestRunOperatorReturnsErrorWhenManagerCreationFails(t *testing.T) {
	restoreArgs := setOperatorArgs(t,
		"operator",
		"--metrics-bind-address=:9090",
		"--health-probe-bind-address=:9091",
		"--enable-webhooks=false",
		"--leader-elect=true",
	)
	defer restoreArgs()

	fs := flag.NewFlagSet("operator", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	managerCreated := false
	code := runOperator(fs, operatorRuntime{
		getConfig: func() (*rest.Config, error) {
			return &rest.Config{Host: "https://kubernetes.example.test"}, nil
		},
		newManager: func(cfg *rest.Config, opts ctrl.Options) (ctrl.Manager, error) {
			managerCreated = true
			if cfg.Host != "https://kubernetes.example.test" {
				t.Fatalf("manager config host = %q, want test host", cfg.Host)
			}
			if opts.Metrics.BindAddress != ":9090" {
				t.Fatalf("metrics bind address = %q, want :9090", opts.Metrics.BindAddress)
			}
			if opts.HealthProbeBindAddress != ":9091" {
				t.Fatalf("health probe bind address = %q, want :9091", opts.HealthProbeBindAddress)
			}
			if !opts.LeaderElection {
				t.Fatal("expected leader election option to be enabled")
			}
			return nil, errors.New("manager creation failed")
		},
	})

	if code != 1 {
		t.Fatalf("runOperator exit code = %d, want 1", code)
	}
	if !managerCreated {
		t.Fatal("expected manager factory to be called")
	}
}

func setOperatorArgs(t *testing.T, args ...string) func() {
	t.Helper()
	oldArgs := os.Args
	os.Args = args
	return func() {
		os.Args = oldArgs
	}
}
