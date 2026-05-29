// Package main is the entry point for the arca-lb operator (Kubernetes controller).
package main

import (
	"flag"
	"log/slog"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	"github.com/akam1o/arca-lb/internal/operator/controller"
	"github.com/akam1o/arca-lb/internal/operator/webhook"
)

var scheme = runtime.NewScheme()

type operatorOptions struct {
	metricsAddr              string
	probeAddr                string
	enableWebhooks           bool
	enableLeaderElection     bool
	agentStatusTTL           time.Duration
	agentStatusPruneInterval time.Duration
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	os.Exit(runOperator(flag.CommandLine, defaultOperatorRuntime()))
}

type operatorRuntime struct {
	getConfig  func() (*rest.Config, error)
	newManager func(*rest.Config, ctrl.Options) (ctrl.Manager, error)
}

func defaultOperatorRuntime() operatorRuntime {
	return operatorRuntime{
		getConfig:  ctrl.GetConfig,
		newManager: ctrl.NewManager,
	}
}

func runOperator(fs *flag.FlagSet, rt operatorRuntime) int {
	var opts operatorOptions
	var zapOpts zap.Options
	bindOperatorFlags(fs, &opts, &zapOpts)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 1
	}

	ctrllog.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	logger := slog.Default()

	cfg, err := rt.getConfig()
	if err != nil {
		logger.Error("unable to get Kubernetes config", "error", err)
		return 1
	}

	mgr, err := rt.newManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: opts.metricsAddr,
		},
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			Port: 9443,
		}),
		HealthProbeBindAddress: opts.probeAddr,
		LeaderElection:         opts.enableLeaderElection,
		LeaderElectionID:       "arca-lb-operator.arca.io",
	})
	if err != nil {
		logger.Error("unable to start manager", "error", err)
		return 1
	}

	// Register VirtualIP controller
	if err := (&controller.VirtualIPReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		AgentStatusTTL:          opts.agentStatusTTL,
		AgentStatusRequeueAfter: opts.agentStatusPruneInterval,
	}).SetupWithManager(mgr); err != nil {
		logger.Error("unable to create controller", "controller", "VirtualIP", "error", err)
		return 1
	}

	// Register webhooks
	if opts.enableWebhooks {
		if err := (&webhook.VirtualIPValidator{}).SetupWithManager(mgr); err != nil {
			logger.Error("unable to create webhook", "webhook", "VirtualIP", "error", err)
			return 1
		}
	}

	// Health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error("unable to set up health check", "error", err)
		return 1
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error("unable to set up ready check", "error", err)
		return 1
	}

	logger.Info("starting operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error("unable to run manager", "error", err)
		return 1
	}
	return 0
}

func bindOperatorFlags(fs *flag.FlagSet, opts *operatorOptions, zapOpts *zap.Options) {
	fs.StringVar(&opts.metricsAddr, "metrics-bind-address", "127.0.0.1:8080", "The address the metrics endpoint binds to.")
	fs.StringVar(&opts.probeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	fs.BoolVar(&opts.enableWebhooks, "enable-webhooks", false, "Enable admission webhooks.")
	fs.BoolVar(&opts.enableLeaderElection, "leader-elect", false, "Enable leader election.")
	fs.DurationVar(&opts.agentStatusTTL, "agent-status-ttl", 0, "Fallback maximum age for per-agent VirtualIP status observations without ttlSeconds. Zero uses the default.")
	fs.DurationVar(&opts.agentStatusPruneInterval, "agent-status-prune-interval", 0, "How often to recheck VirtualIPs with current per-agent status. Zero uses half of --agent-status-ttl.")
	zapOpts.BindFlags(fs)
}
