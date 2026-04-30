// Package watcher provides a Kubernetes informer-based watcher
// for VirtualIP CRDs. It replaces the gRPC-based config sync.
package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	runtimescheme "sigs.k8s.io/controller-runtime/pkg/scheme"
)

// EventType distinguishes watch event types.
type EventType int

const (
	EventAdd EventType = iota
	EventUpdate
	EventDelete
)

// Event represents a VirtualIP change observed by the watcher.
type Event struct {
	Type EventType
	VIP  *v1alpha1.VirtualIP
}

// Handler processes VirtualIP events.
type Handler interface {
	OnVIPUpdate(vip *v1alpha1.VirtualIP)
	OnVIPDelete(vip *v1alpha1.VirtualIP)
}

// Config configures the K8s watcher.
type Config struct {
	// Kubeconfig path. Empty uses in-cluster config.
	Kubeconfig string
	// Namespace to watch. Empty watches all namespaces.
	Namespace string
	// ResyncInterval is how often the informer re-lists.
	ResyncInterval time.Duration
}

// Watcher watches VirtualIP CRDs via K8s informers.
type Watcher struct {
	config  Config
	handler Handler
	logger  *slog.Logger
	cache   cache.Cache
	scheme  *runtime.Scheme
}

// New creates a new CRD watcher.
func New(cfg Config, handler Handler, logger *slog.Logger) (*Watcher, error) {
	if cfg.ResyncInterval == 0 {
		cfg.ResyncInterval = 30 * time.Second
	}

	scheme := runtime.NewScheme()
	sb := &runtimescheme.Builder{GroupVersion: v1alpha1.GroupVersion}
	sb.Register(&v1alpha1.VirtualIP{}, &v1alpha1.VirtualIPList{})
	if err := sb.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add scheme: %w", err)
	}

	return &Watcher{
		config:  cfg,
		handler: handler,
		logger:  logger.With("component", "watcher"),
		scheme:  scheme,
	}, nil
}

// Start starts watching VirtualIP CRDs. Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	restCfg, err := w.buildRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to build REST config: %w", err)
	}

	httpClient, err := rest.HTTPClientFor(restCfg)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	mapper, err := apiutil.NewDynamicRESTMapper(restCfg, httpClient)
	if err != nil {
		return fmt.Errorf("failed to create REST mapper: %w", err)
	}

	opts := cache.Options{
		Scheme:     w.scheme,
		Mapper:     mapper,
		SyncPeriod: &w.config.ResyncInterval,
	}
	if w.config.Namespace != "" {
		opts.DefaultNamespaces = map[string]cache.Config{
			w.config.Namespace: {},
		}
	}

	c, err := cache.New(restCfg, opts)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	w.cache = c

	// Get the informer for VirtualIP and register event handler
	informer, err := c.GetInformer(ctx, &v1alpha1.VirtualIP{})
	if err != nil {
		return fmt.Errorf("failed to get informer: %w", err)
	}

	reg, err := informer.AddEventHandler(&eventHandler{
		handler: w.handler,
		logger:  w.logger,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}
	defer func() {
		if err := informer.RemoveEventHandler(reg); err != nil {
			w.logger.Warn("failed to remove event handler", "error", err)
		}
	}()

	w.logger.Info("starting VirtualIP watcher", "namespace", w.config.Namespace)

	// Start cache (blocks until context is cancelled)
	return c.Start(ctx)
}

// GetClient returns a read-only client for querying VirtualIP resources.
func (w *Watcher) GetClient() (client.Reader, error) {
	if w.cache == nil {
		return nil, fmt.Errorf("watcher not started")
	}
	return w.cache, nil
}

func (w *Watcher) buildRESTConfig() (*rest.Config, error) {
	if w.config.Kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", w.config.Kubeconfig)
	}
	return rest.InClusterConfig()
}

// --- event handler ---

type eventHandler struct {
	handler Handler
	logger  *slog.Logger
}

func (h *eventHandler) OnAdd(obj interface{}, _ bool) {
	vip, ok := obj.(*v1alpha1.VirtualIP)
	if !ok {
		return
	}
	h.logger.Debug("VirtualIP added", "name", vip.Name, "namespace", vip.Namespace)
	h.handler.OnVIPUpdate(vip)
}

func (h *eventHandler) OnUpdate(_, newObj interface{}) {
	vip, ok := newObj.(*v1alpha1.VirtualIP)
	if !ok {
		return
	}
	h.logger.Debug("VirtualIP updated", "name", vip.Name, "namespace", vip.Namespace)
	h.handler.OnVIPUpdate(vip)
}

func (h *eventHandler) OnDelete(obj interface{}) {
	vip, ok := obj.(*v1alpha1.VirtualIP)
	if !ok {
		tombstone, ok := obj.(k8scache.DeletedFinalStateUnknown)
		if !ok {
			h.logger.Warn("received unexpected delete event object", "type", fmt.Sprintf("%T", obj))
			return
		}

		vip, ok = tombstone.Obj.(*v1alpha1.VirtualIP)
		if !ok {
			h.logger.Warn("received unexpected delete tombstone object", "type", fmt.Sprintf("%T", tombstone.Obj))
			return
		}
	}
	h.logger.Debug("VirtualIP deleted", "name", vip.Name, "namespace", vip.Namespace)
	h.handler.OnVIPDelete(vip)
}
