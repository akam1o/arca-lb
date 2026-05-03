package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
)

// ChangeLogRecord represents a change log record
type ChangeLogRecord struct {
	ID        int64     `gorm:"primaryKey"`
	Revision  int64     `gorm:"not null;index"`
	EventType string    `gorm:"type:enum('vip_created','vip_updated','vip_deleted','backend_added','backend_updated','backend_deleted');not null"`
	VIPID     *string   `gorm:"type:char(36)"`
	BackendID *string   `gorm:"type:char(36)"`
	CreatedAt time.Time `gorm:"not null;index"`
}

// TableName returns the table name for ChangeLogRecord
func (ChangeLogRecord) TableName() string {
	return "change_log"
}

// Watch watches for changes in VIPs and Backends using polling
func (ds *MySQLDataStore) Watch(ctx context.Context) (<-chan datastore.WatchEvent, error) {
	lastID, err := ds.latestChangeLogID(ctx)
	if err != nil {
		return nil, err
	}

	eventChan := make(chan datastore.WatchEvent, 100)

	go func() {
		defer close(eventChan)

		pollInterval := 100 * time.Millisecond

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				// Poll for new changes
				var changes []ChangeLogRecord
				if err := ds.db.WithContext(ctx).Where("id > ?", lastID).
					Order("id ASC").
					Limit(100).
					Find(&changes).Error; err != nil {
					eventChan <- datastore.WatchEvent{
						Type:  datastore.EventTypeError,
						Error: fmt.Errorf("failed to poll changes: %w", err),
					}
					continue
				}

				// Process each change
				for _, change := range changes {
					event, err := ds.buildWatchEvent(ctx, change)
					if err != nil {
						eventChan <- datastore.WatchEvent{
							Type:  datastore.EventTypeError,
							Error: fmt.Errorf("failed to build watch event: %w", err),
						}
						lastID = change.ID
						continue
					}

					if event != nil {
						eventChan <- *event
					}

					lastID = change.ID
				}
			}
		}
	}()

	return eventChan, nil
}

func (ds *MySQLDataStore) latestChangeLogID(ctx context.Context) (int64, error) {
	var lastID int64
	if err := ds.db.WithContext(ctx).
		Raw("SELECT COALESCE(MAX(id), 0) FROM change_log").
		Scan(&lastID).Error; err != nil {
		return 0, fmt.Errorf("failed to get latest change log id: %w", err)
	}
	return lastID, nil
}

// buildWatchEvent builds a WatchEvent from a ChangeLogRecord
func (ds *MySQLDataStore) buildWatchEvent(ctx context.Context, change ChangeLogRecord) (*datastore.WatchEvent, error) {
	event := &datastore.WatchEvent{
		Revision: change.Revision,
	}

	// Map event type
	switch change.EventType {
	case "vip_created":
		event.Type = datastore.EventTypeVIPCreated
		if change.VIPID != nil {
			vip, err := ds.GetVIP(ctx, *change.VIPID)
			if err != nil {
				return nil, fmt.Errorf("failed to get VIP: %w", err)
			}
			event.VIP = vip
		}

	case "vip_updated":
		event.Type = datastore.EventTypeVIPUpdated
		if change.VIPID != nil {
			vip, err := ds.GetVIP(ctx, *change.VIPID)
			if err != nil {
				return nil, fmt.Errorf("failed to get VIP: %w", err)
			}
			event.VIP = vip
		}

	case "vip_deleted":
		event.Type = datastore.EventTypeVIPDeleted
		if change.VIPID != nil {
			event.VIP = &models.VIP{ID: *change.VIPID}
		}

	case "backend_added":
		event.Type = datastore.EventTypeBackendAdded
		if change.BackendID != nil {
			backend, err := ds.GetBackend(ctx, *change.BackendID)
			if err != nil {
				return nil, fmt.Errorf("failed to get backend: %w", err)
			}
			event.Backend = backend
		}

	case "backend_updated":
		event.Type = datastore.EventTypeBackendUpdated
		if change.BackendID != nil {
			backend, err := ds.GetBackend(ctx, *change.BackendID)
			if err != nil {
				return nil, fmt.Errorf("failed to get backend: %w", err)
			}
			event.Backend = backend
		}

	case "backend_deleted":
		event.Type = datastore.EventTypeBackendDeleted
		if change.BackendID != nil && change.VIPID != nil {
			event.Backend = &models.Backend{
				ID:    *change.BackendID,
				VIPID: *change.VIPID,
			}
		}

	default:
		return nil, fmt.Errorf("unknown event type: %s", change.EventType)
	}

	return event, nil
}
