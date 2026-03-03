package etcd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	// DefaultDialTimeout is the default timeout for etcd client dial
	DefaultDialTimeout = 5 * time.Second

	// DefaultKeyPrefix is the default key prefix for etcd keys
	DefaultKeyPrefix = "/arca-lb"

	// DefaultRequestTimeout is the default timeout for etcd requests
	DefaultRequestTimeout = 3 * time.Second
)

// Register etcd datastore factory on package init
func init() {
	datastore.Register("etcd", NewEtcdDataStore)
}

// EtcdDataStore implements datastore.DataStore interface using etcd
type EtcdDataStore struct {
	client         *clientv3.Client
	keyPrefix      string
	requestTimeout time.Duration
}

// NewEtcdDataStore creates a new etcd datastore instance
func NewEtcdDataStore(ctx context.Context, cfg *datastore.Config) (datastore.DataStore, error) {
	dialTimeout := DefaultDialTimeout
	if cfg.EtcdDialTimeout > 0 {
		dialTimeout = cfg.EtcdDialTimeout
	}

	config := clientv3.Config{
		Endpoints:   cfg.EtcdEndpoints,
		DialTimeout: dialTimeout,
	}

	// TLS configuration if enabled
	if cfg.EtcdTLS {
		tlsConfig, err := loadTLSConfig(cfg.EtcdCertFile, cfg.EtcdKeyFile, cfg.EtcdCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS config: %w", err)
		}
		config.TLS = tlsConfig
	}

	client, err := clientv3.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	// Set default key prefix if not specified
	keyPrefix := cfg.EtcdKeyPrefix
	if keyPrefix == "" {
		keyPrefix = DefaultKeyPrefix
	}

	// Set request timeout
	requestTimeout := DefaultRequestTimeout
	if cfg.EtcdRequestTimeout > 0 {
		requestTimeout = cfg.EtcdRequestTimeout
	}

	ds := &EtcdDataStore{
		client:         client,
		keyPrefix:      keyPrefix,
		requestTimeout: requestTimeout,
	}

	// Initialize revision if not exists
	if err := ds.initRevision(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to initialize revision: %w", err)
	}

	return ds, nil
}

// loadTLSConfig loads TLS configuration from files
func loadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}, nil
}

// Close closes the etcd client connection
func (ds *EtcdDataStore) Close() error {
	if ds.client != nil {
		return ds.client.Close()
	}
	return nil
}

// GetConfig retrieves the complete configuration for agent distribution
func (ds *EtcdDataStore) GetConfig(ctx context.Context) (*models.Config, error) {
	vips, err := ds.ListVIPs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list VIPs: %w", err)
	}

	revision, err := ds.GetRevision(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get revision: %w", err)
	}

	config := &models.Config{
		Revision: revision,
		VIPs:     make([]models.VIPConfig, 0, len(vips)),
	}

	for _, vip := range vips {
		backends, err := ds.ListBackends(ctx, vip.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to list backends for VIP %s: %w", vip.ID, err)
		}

		vipConfig := models.VIPConfig{
			VIP:         vip,
			HealthCheck: vip.HealthCheck,
			Backends:    backends,
		}
		config.VIPs = append(config.VIPs, vipConfig)
	}

	return config, nil
}

// vipKey returns the etcd key for a VIP
func (ds *EtcdDataStore) vipKey(id string) string {
	return fmt.Sprintf("%s/vips/%s", ds.keyPrefix, id)
}

// vipPrefix returns the etcd key prefix for all VIPs
func (ds *EtcdDataStore) vipPrefix() string {
	return fmt.Sprintf("%s/vips/", ds.keyPrefix)
}

// backendKey returns the etcd key for a backend
func (ds *EtcdDataStore) backendKey(vipID, backendID string) string {
	return fmt.Sprintf("%s/backends/%s/%s", ds.keyPrefix, vipID, backendID)
}

// backendPrefix returns the etcd key prefix for backends of a VIP
func (ds *EtcdDataStore) backendPrefix(vipID string) string {
	return fmt.Sprintf("%s/backends/%s/", ds.keyPrefix, vipID)
}

// revisionKey returns the etcd key for revision
func (ds *EtcdDataStore) revisionKey() string {
	return fmt.Sprintf("%s/revision", ds.keyPrefix)
}

// backendIndexKey returns the etcd key for backend ID to VIP ID mapping
func (ds *EtcdDataStore) backendIndexKey(backendID string) string {
	return fmt.Sprintf("%s/backend-index/%s", ds.keyPrefix, backendID)
}
