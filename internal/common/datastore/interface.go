package datastore

import (
	"context"

	"github.com/akam1o/arca-lb/internal/common/models"
)

// DataStore is the abstract interface for pluggable datastores.
// Implementations should be provided for MySQL and etcd.
type DataStore interface {
	VIPStore
	BackendStore
	RevisionStore
	ConfigStore
	WatchStore
	TransactionStore
	CloseStore
}

// VIPStore defines VirtualIP persistence operations.
type VIPStore interface {
	CreateVIP(ctx context.Context, vip *models.VIP) error
	GetVIP(ctx context.Context, id string) (*models.VIP, error)
	ListVIPs(ctx context.Context) ([]models.VIP, error)
	UpdateVIP(ctx context.Context, vip *models.VIP) error
	DeleteVIP(ctx context.Context, id string) error
}

// BackendStore defines backend persistence operations.
type BackendStore interface {
	AddBackend(ctx context.Context, backend *models.Backend) error
	GetBackend(ctx context.Context, id string) (*models.Backend, error)
	ListBackends(ctx context.Context, vipID string) ([]models.Backend, error)
	UpdateBackend(ctx context.Context, backend *models.Backend) error
	DeleteBackend(ctx context.Context, id string) error
}

// RevisionReader reads the datastore revision.
type RevisionReader interface {
	GetRevision(ctx context.Context) (int64, error)
}

// RevisionStore defines revision management operations.
type RevisionStore interface {
	RevisionReader
	IncrementRevision(ctx context.Context) (int64, error)
}

// ConfigStore retrieves full configuration snapshots for agent distribution.
type ConfigStore interface {
	GetConfig(ctx context.Context) (*models.Config, error)
}

// WatchStore watches for real-time change notifications.
type WatchStore interface {
	Watch(ctx context.Context) (<-chan WatchEvent, error)
}

// TransactionStore starts datastore transactions.
type TransactionStore interface {
	BeginTx(ctx context.Context) (Transaction, error)
}

// CloseStore releases datastore resources.
type CloseStore interface {
	Close() error
}

// ControllerStore is the datastore surface required by the REST controller API.
type ControllerStore interface {
	VIPStore
	BackendStore
	RevisionReader
}

// ConfigSyncStore is the datastore surface required by the gRPC config sync API.
type ConfigSyncStore interface {
	RevisionReader
	ConfigStore
	WatchStore
}

// Transaction defines transactional operations.
type Transaction interface {
	CreateVIP(ctx context.Context, vip *models.VIP) error
	AddBackend(ctx context.Context, backend *models.Backend) error
	UpdateVIP(ctx context.Context, vip *models.VIP) error
	DeleteVIP(ctx context.Context, id string) error

	Commit() error
	Rollback() error
}

// WatchEvent is a change notification event.
type WatchEvent struct {
	Type     WatchEventType
	Revision int64
	VIP      *models.VIP
	Backend  *models.Backend
	Error    error
}

// WatchEventType is the type of change event.
type WatchEventType int

const (
	EventTypeVIPCreated WatchEventType = iota
	EventTypeVIPUpdated
	EventTypeVIPDeleted
	EventTypeBackendAdded
	EventTypeBackendUpdated
	EventTypeBackendDeleted
	EventTypeError
)
