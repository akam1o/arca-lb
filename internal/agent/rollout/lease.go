// Package rollout provides cluster-wide coordination for disruptive VIP changes.
package rollout

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultNamespace      = "arca-lb-system"
	defaultLeaseDuration  = 2 * time.Minute
	defaultRetryInterval  = time.Second
	defaultReleaseTimeout = 5 * time.Second

	holderInstanceBytes     = 8
	maxHolderIdentityLength = 128
)

var coordinatorInstanceCounter uint64

// Config controls Kubernetes Lease based rollout coordination.
type Config struct {
	Kubeconfig     string
	Namespace      string
	HolderIdentity string
	LeaseDuration  time.Duration
	RetryInterval  time.Duration
	ReleaseTimeout time.Duration
}

// Coordinator serializes disruptive VIP work through Kubernetes Lease objects.
type Coordinator struct {
	client         client.Client
	namespace      string
	holderIdentity string
	leaseDuration  time.Duration
	retryInterval  time.Duration
	releaseTimeout time.Duration
	logger         *slog.Logger
	now            func() time.Time

	localMu    sync.Mutex
	localLocks map[string]*localLeaseLock
}

type heldLease struct {
	mu    sync.Mutex
	lease *coordinationv1.Lease
}

type localLeaseLock struct {
	ch   chan struct{}
	refs int
}

// New creates a Kubernetes-backed rollout coordinator.
func New(cfg Config, logger *slog.Logger) (*Coordinator, error) {
	restCfg, err := buildRESTConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build rollout coordinator REST config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add coordination scheme: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create rollout coordinator client: %w", err)
	}

	return NewWithClient(k8sClient, cfg, logger), nil
}

// NewWithClient creates a coordinator with an existing client. It is primarily used by tests.
func NewWithClient(k8sClient client.Client, cfg Config, logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
	}
	if namespace == "" {
		namespace = defaultNamespace
	}

	holder := cfg.HolderIdentity
	if holder == "" {
		if hostname, err := os.Hostname(); err == nil {
			holder = hostname
		}
	}
	if holder == "" {
		holder = "unknown"
	}
	holder = leaseHolderIdentity(holder, newCoordinatorInstanceID())

	leaseDuration := cfg.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = defaultLeaseDuration
	}
	retryInterval := cfg.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultRetryInterval
	}
	releaseTimeout := cfg.ReleaseTimeout
	if releaseTimeout <= 0 {
		releaseTimeout = defaultReleaseTimeout
	}

	return &Coordinator{
		client:         k8sClient,
		namespace:      namespace,
		holderIdentity: holder,
		leaseDuration:  leaseDuration,
		retryInterval:  retryInterval,
		releaseTimeout: releaseTimeout,
		logger:         logger.With("component", "rollout-coordinator"),
		now:            time.Now,
		localLocks:     make(map[string]*localLeaseLock),
	}
}

// RunExclusive runs fn while holding the Lease for key.
func (c *Coordinator) RunExclusive(ctx context.Context, key string, fn func(context.Context) error) error {
	if c == nil || c.client == nil {
		return fn(ctx)
	}

	unlockLocal, err := c.acquireLocal(ctx, key)
	if err != nil {
		return err
	}
	defer unlockLocal()

	lease, err := c.acquire(ctx, key)
	if err != nil {
		return err
	}
	held := &heldLease{lease: lease}

	opCtx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()

	done := make(chan struct{})
	renewErrCh := make(chan error, 1)
	go c.renewLoop(opCtx, key, held, done, renewErrCh, cancelOp)

	err = fn(opCtx)
	close(done)

	select {
	case renewErr := <-renewErrCh:
		if err == nil {
			err = renewErr
		}
	default:
	}

	releaseCtx, cancelRelease := context.WithTimeout(context.Background(), c.releaseTimeout)
	defer cancelRelease()
	if releaseErr := c.release(releaseCtx, key, held); releaseErr != nil && err == nil {
		err = releaseErr
	}
	return err
}

func (c *Coordinator) acquireLocal(ctx context.Context, key string) (func(), error) {
	c.localMu.Lock()
	if c.localLocks == nil {
		c.localLocks = make(map[string]*localLeaseLock)
	}
	lock := c.localLocks[key]
	if lock == nil {
		lock = &localLeaseLock{ch: make(chan struct{}, 1)}
		c.localLocks[key] = lock
	}
	lock.refs++
	c.localMu.Unlock()

	select {
	case lock.ch <- struct{}{}:
		return func() {
			<-lock.ch
			c.releaseLocalRef(key, lock)
		}, nil
	case <-ctx.Done():
		c.releaseLocalRef(key, lock)
		return nil, ctx.Err()
	}
}

func (c *Coordinator) releaseLocalRef(key string, lock *localLeaseLock) {
	c.localMu.Lock()
	defer c.localMu.Unlock()

	lock.refs--
	if lock.refs == 0 {
		delete(c.localLocks, key)
	}
}

func (c *Coordinator) acquire(ctx context.Context, key string) (*coordinationv1.Lease, error) {
	name := LeaseName(key)
	for {
		lease, acquired, err := c.tryAcquire(ctx, key, name)
		if err != nil {
			return nil, err
		}
		if acquired {
			c.logger.Debug("acquired rollout lease", "key", key, "lease", name)
			return lease, nil
		}

		timer := time.NewTimer(c.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Coordinator) tryAcquire(ctx context.Context, key, name string) (*coordinationv1.Lease, bool, error) {
	var lease coordinationv1.Lease
	err := c.client.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: name}, &lease)
	if apierrors.IsNotFound(err) {
		created, err := c.createLease(ctx, key, name)
		if apierrors.IsAlreadyExists(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return created, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !c.leaseAvailable(&lease) {
		return nil, false, nil
	}

	previousHolder := valueOrEmpty(lease.Spec.HolderIdentity)
	c.claimLease(&lease, previousHolder)
	if err := c.client.Update(ctx, &lease); err != nil {
		if apierrors.IsConflict(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return lease.DeepCopy(), true, nil
}

func (c *Coordinator) createLease(ctx context.Context, key, name string) (*coordinationv1.Lease, error) {
	now := metav1.NewMicroTime(c.now())
	duration := leaseDurationSeconds(c.leaseDuration)
	transitions := int32(0)
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: c.namespace,
			Name:      name,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "arca-lb",
				"app.kubernetes.io/component": "rollout-coordinator",
				"arca.io/rollout-key-hash":    keyHash(key),
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &c.holderIdentity,
			LeaseDurationSeconds: &duration,
			AcquireTime:          &now,
			RenewTime:            &now,
			LeaseTransitions:     &transitions,
		},
	}
	if err := c.client.Create(ctx, lease); err != nil {
		return nil, err
	}
	return lease.DeepCopy(), nil
}

func (c *Coordinator) claimLease(lease *coordinationv1.Lease, previousHolder string) {
	now := metav1.NewMicroTime(c.now())
	duration := leaseDurationSeconds(c.leaseDuration)
	transitions := int32(0)
	if lease.Spec.LeaseTransitions != nil {
		transitions = *lease.Spec.LeaseTransitions
	}
	if previousHolder != "" && previousHolder != c.holderIdentity {
		transitions++
	}

	lease.Spec.HolderIdentity = &c.holderIdentity
	lease.Spec.LeaseDurationSeconds = &duration
	if lease.Spec.AcquireTime == nil || previousHolder != c.holderIdentity {
		lease.Spec.AcquireTime = &now
	}
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseTransitions = &transitions
}

func (c *Coordinator) leaseAvailable(lease *coordinationv1.Lease) bool {
	holder := valueOrEmpty(lease.Spec.HolderIdentity)
	if holder == "" || holder == c.holderIdentity {
		return true
	}

	renewedAt := lease.Spec.RenewTime
	if renewedAt == nil {
		renewedAt = lease.Spec.AcquireTime
	}
	if renewedAt == nil {
		return true
	}
	duration := c.leaseDuration
	if lease.Spec.LeaseDurationSeconds != nil && *lease.Spec.LeaseDurationSeconds > 0 {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	return !renewedAt.Time.Add(duration).After(c.now())
}

func (c *Coordinator) renewLoop(ctx context.Context, key string, held *heldLease, done <-chan struct{}, errCh chan<- error, cancel context.CancelFunc) {
	interval := c.leaseDuration / 3
	if interval <= 0 {
		interval = c.retryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := c.renew(ctx, key, held); err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (c *Coordinator) renew(ctx context.Context, key string, held *heldLease) error {
	name := LeaseName(key)
	var updated *coordinationv1.Lease
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var lease coordinationv1.Lease
		if err := c.client.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: name}, &lease); err != nil {
			return err
		}
		if valueOrEmpty(lease.Spec.HolderIdentity) != c.holderIdentity {
			return fmt.Errorf("rollout lease %s/%s is held by %q", c.namespace, name, valueOrEmpty(lease.Spec.HolderIdentity))
		}
		now := metav1.NewMicroTime(c.now())
		duration := leaseDurationSeconds(c.leaseDuration)
		lease.Spec.RenewTime = &now
		lease.Spec.LeaseDurationSeconds = &duration
		if err := c.client.Update(ctx, &lease); err != nil {
			return err
		}
		updated = lease.DeepCopy()
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to renew rollout lease for %s: %w", key, err)
	}
	held.mu.Lock()
	held.lease = updated
	held.mu.Unlock()
	return nil
}

func (c *Coordinator) release(ctx context.Context, key string, held *heldLease) error {
	name := LeaseName(key)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var lease coordinationv1.Lease
		if err := c.client.Get(ctx, types.NamespacedName{Namespace: c.namespace, Name: name}, &lease); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if valueOrEmpty(lease.Spec.HolderIdentity) != c.holderIdentity {
			return nil
		}
		now := metav1.NewMicroTime(c.now())
		lease.Spec.HolderIdentity = nil
		lease.Spec.RenewTime = &now
		return c.client.Update(ctx, &lease)
	})
	if err != nil {
		return fmt.Errorf("failed to release rollout lease for %s: %w", key, err)
	}
	held.mu.Lock()
	held.lease = nil
	held.mu.Unlock()
	c.logger.Debug("released rollout lease", "key", key, "lease", name)
	return nil
}

// LeaseName returns the Kubernetes Lease name for a logical rollout key.
func LeaseName(key string) string {
	return "arca-lb-rollout-" + keyHash(key)
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

func leaseDurationSeconds(d time.Duration) int32 {
	seconds := int64(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	if seconds > math.MaxInt32 {
		seconds = math.MaxInt32
	}
	return int32(seconds)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func newCoordinatorInstanceID() string {
	seq := atomic.AddUint64(&coordinatorInstanceCounter, 1)
	var b [holderInstanceBytes]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%s-%d", hex.EncodeToString(b[:]), seq)
	}
	return fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), seq)
}

func leaseHolderIdentity(base, instance string) string {
	suffix := "/" + instance
	if len(base)+len(suffix) <= maxHolderIdentityLength {
		return base + suffix
	}
	if len(suffix) >= maxHolderIdentityLength {
		return suffix[len(suffix)-maxHolderIdentityLength:]
	}
	return base[:maxHolderIdentityLength-len(suffix)] + suffix
}

func buildRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
