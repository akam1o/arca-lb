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
	MySQLHost     string
	MySQLPort     int
	MySQLUser     string
	MySQLPassword string
	MySQLDatabase string

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
	factory, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported datastore type: %s", cfg.Type)
	}
	return factory(ctx, cfg)
}
