package datastore

import (
	"context"
	"fmt"
	"time"
)

// Config is the datastore configuration.
type Config struct {
	Type string // "mysql" or "etcd"

	// MySQL settings.
	MySQLHost              string
	MySQLPort              int
	MySQLUser              string
	MySQLPassword          string
	MySQLDatabase          string
	MySQLTLSMode           string
	MySQLTLSCAFile         string
	MySQLTLSCertFile       string
	MySQLTLSKeyFile        string
	MySQLTLSServerName     string
	MySQLMaxOpenConns      int
	MySQLMaxIdleConns      int
	MySQLConnMaxLifetime   time.Duration
	MySQLWatchPollInterval time.Duration

	// etcd settings.
	EtcdEndpoints      []string
	EtcdKeyPrefix      string
	EtcdTLS            bool
	EtcdCertFile       string
	EtcdKeyFile        string
	EtcdCAFile         string
	EtcdDialTimeout    time.Duration
	EtcdRequestTimeout time.Duration
}

// DataStoreFactory is a function that creates a DataStore instance
type DataStoreFactory func(ctx context.Context, cfg *Config) (DataStore, error)

// Registry holds registered datastore factories
var registry = make(map[string]DataStoreFactory)

// Register registers a datastore factory
func Register(name string, factory DataStoreFactory) {
	registry[name] = factory
}

// NewDataStore is the datastore factory function.
func NewDataStore(ctx context.Context, cfg *Config) (DataStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("datastore config is required")
	}
	factory, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported datastore type: %s", cfg.Type)
	}
	return factory(ctx, cfg)
}
