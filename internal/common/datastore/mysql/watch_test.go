package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBuildWatchEventSkipsMissingVIPSnapshot(t *testing.T) {
	for _, eventType := range []string{"vip_created", "vip_updated"} {
		t.Run(eventType, func(t *testing.T) {
			db, mock, cleanup := newMockGORMDB(t)
			defer cleanup()

			vipID := "vip-1"
			expectMissingVIP(mock, vipID)

			ds := &MySQLDataStore{db: db}
			event, err := ds.buildWatchEvent(context.Background(), ChangeLogRecord{
				ID:        10,
				Revision:  20,
				EventType: eventType,
				VIPID:     &vipID,
			})
			if err != nil {
				t.Fatalf("buildWatchEvent error = %v, want nil", err)
			}
			if event != nil {
				t.Fatalf("event = %#v, want nil for missing VIP snapshot", event)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet SQL expectations: %v", err)
			}
		})
	}
}

func TestBuildWatchEventSkipsMissingBackendSnapshot(t *testing.T) {
	for _, eventType := range []string{"backend_added", "backend_updated"} {
		t.Run(eventType, func(t *testing.T) {
			db, mock, cleanup := newMockGORMDB(t)
			defer cleanup()

			backendID := "backend-1"
			expectMissingBackend(mock, backendID)

			ds := &MySQLDataStore{db: db}
			event, err := ds.buildWatchEvent(context.Background(), ChangeLogRecord{
				ID:        10,
				Revision:  20,
				EventType: eventType,
				BackendID: &backendID,
			})
			if err != nil {
				t.Fatalf("buildWatchEvent error = %v, want nil", err)
			}
			if event != nil {
				t.Fatalf("event = %#v, want nil for missing backend snapshot", event)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet SQL expectations: %v", err)
			}
		})
	}
}

func expectMissingVIP(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery("SELECT \\* FROM `vips` WHERE id = \\? ORDER BY `vips`\\.`id` LIMIT \\?").
		WithArgs(id, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"vip",
			"port",
			"protocol",
			"lb_method",
			"encap_type",
			"dscp",
			"created_at",
			"updated_at",
		}))
}

func expectMissingBackend(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery("SELECT \\* FROM `backends` WHERE id = \\? ORDER BY `backends`\\.`id` LIMIT \\?").
		WithArgs(id, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"vip_id",
			"ip",
			"weight",
			"created_at",
			"updated_at",
		}))
}
