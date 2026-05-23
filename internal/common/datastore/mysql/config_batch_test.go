package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestListVIPsBatchesHealthChecks(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	now := time.Unix(100, 0).UTC()
	mock.ExpectQuery("SELECT \\* FROM `vips`").
		WillReturnRows(vipRows().
			AddRow("vip-1", "192.0.2.10", 80, "TCP", "maglev", nil, nil, now, now).
			AddRow("vip-2", "192.0.2.11", 443, "TCP", "maglev", nil, nil, now, now))
	mock.ExpectQuery("SELECT \\* FROM `health_checks` WHERE vip_id IN \\(\\?,\\?\\)").
		WithArgs("vip-1", "vip-2").
		WillReturnRows(healthCheckRows().
			AddRow("hc-1", "vip-1", "tcp", 5, 3, 3, 2, []byte(`{}`), now, now))

	ds := &MySQLDataStore{db: db}
	vips, err := ds.ListVIPs(context.Background())
	if err != nil {
		t.Fatalf("ListVIPs: %v", err)
	}
	if len(vips) != 2 {
		t.Fatalf("VIP count = %d, want 2", len(vips))
	}
	if vips[0].HealthCheck == nil || vips[0].HealthCheck.ID != "hc-1" {
		t.Fatalf("first VIP health check = %#v, want hc-1", vips[0].HealthCheck)
	}
	if vips[1].HealthCheck != nil {
		t.Fatalf("second VIP health check = %#v, want nil", vips[1].HealthCheck)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestGetConfigBatchesHealthChecksAndBackends(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	now := time.Unix(200, 0).UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT revision FROM `system_metadata` LIMIT \\?").
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(42))
	mock.ExpectQuery("SELECT \\* FROM `vips`").
		WillReturnRows(vipRows().
			AddRow("vip-1", "192.0.2.10", 80, "TCP", "maglev", nil, nil, now, now).
			AddRow("vip-2", "192.0.2.11", 443, "TCP", "maglev", nil, nil, now, now))
	mock.ExpectQuery("SELECT \\* FROM `health_checks` WHERE vip_id IN \\(\\?,\\?\\)").
		WithArgs("vip-1", "vip-2").
		WillReturnRows(healthCheckRows().
			AddRow("hc-1", "vip-1", "tcp", 5, 3, 3, 2, []byte(`{}`), now, now))
	mock.ExpectQuery("SELECT \\* FROM `backends` WHERE vip_id IN \\(\\?,\\?\\)").
		WithArgs("vip-1", "vip-2").
		WillReturnRows(backendRows().
			AddRow("backend-1", "vip-1", "10.0.0.1", 100, now, now).
			AddRow("backend-2", "vip-2", "10.0.0.2", 100, now, now))
	mock.ExpectCommit()

	ds := &MySQLDataStore{db: db}
	cfg, err := ds.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Revision != 42 {
		t.Fatalf("revision = %d, want 42", cfg.Revision)
	}
	if len(cfg.VIPs) != 2 {
		t.Fatalf("VIP config count = %d, want 2", len(cfg.VIPs))
	}
	if len(cfg.VIPs[0].Backends) != 1 || cfg.VIPs[0].Backends[0].ID != "backend-1" {
		t.Fatalf("first VIP backends = %#v, want backend-1", cfg.VIPs[0].Backends)
	}
	if len(cfg.VIPs[1].Backends) != 1 || cfg.VIPs[1].Backends[0].ID != "backend-2" {
		t.Fatalf("second VIP backends = %#v, want backend-2", cfg.VIPs[1].Backends)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func vipRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"vip",
		"port",
		"protocol",
		"lb_method",
		"encap_type",
		"dscp",
		"created_at",
		"updated_at",
	})
}

func healthCheckRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"vip_id",
		"type",
		"interval_sec",
		"timeout_sec",
		"rise_count",
		"fall_count",
		"config",
		"created_at",
		"updated_at",
	})
}

func backendRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"vip_id",
		"ip",
		"weight",
		"created_at",
		"updated_at",
	})
}
