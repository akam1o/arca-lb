package datastore

import (
	"context"

	"github.com/akam1o/arca-lb/internal/common/models"
)

// DataStore is the abstract interface for pluggable datastores.
// Implementations should be provided for MySQL and etcd.
type DataStore interface {
	// VIP operations.
	CreateVIP(ctx context.Context, vip *models.VIP) error
	GetVIP(ctx context.Context, id string) (*models.VIP, error)
	ListVIPs(ctx context.Context) ([]models.VIP, error)
	UpdateVIP(ctx context.Context, vip *models.VIP) error
	DeleteVIP(ctx context.Context, id string) error

	// Backend operations.
	AddBackend(ctx context.Context, backend *models.Backend) error
	GetBackend(ctx context.Context, id string) (*models.Backend, error)
	ListBackends(ctx context.Context, vipID string) ([]models.Backend, error)
	UpdateBackend(ctx context.Context, backend *models.Backend) error
	DeleteBackend(ctx context.Context, id string) error

	// Revision management.
	GetRevision(ctx context.Context) (int64, error)
	IncrementRevision(ctx context.Context) (int64, error)

	// Config retrieval for agent distribution.
	GetConfig(ctx context.Context) (*models.Config, error)

	// Watch for real-time change notifications.
	Watch(ctx context.Context) (<-chan WatchEvent, error)

	// Transaction support.
	BeginTx(ctx context.Context) (Transaction, error)

	// Connection management.
	Close() error
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
