package mysql

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestVIPUpdateValuesClearsNullableFields(t *testing.T) {
	dscp := uint8(10)
	encapType := "L3DSR"
	now := time.Now()
	fallback := VIPRecord{
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  "TCP",
		LBMethod:  "maglev",
		EncapType: &encapType,
		DSCP:      &dscp,
		CreatedAt: now,
		UpdatedAt: now,
	}
	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		LBMethod:  models.LBMethodMaglev,
		CreatedAt: now,
		UpdatedAt: now,
	}

	updates := vipUpdateValues(vip, fallback)

	if got := updates["encap_type"]; got != nil {
		t.Fatalf("encap_type update = %#v, want nil", got)
	}
	if got := updates["dscp"]; got != nil {
		t.Fatalf("dscp update = %#v, want nil", got)
	}
}

func TestVIPUpdateValuesUsesNullableValuesWhenPresent(t *testing.T) {
	dscp := uint8(20)
	now := time.Now()
	vip := &models.VIP{
		ID:        "vip-1",
		VIP:       "192.0.2.10",
		Port:      80,
		Protocol:  models.ProtocolTCP,
		LBMethod:  models.LBMethodMaglev,
		EncapType: models.EncapTypeGRE4,
		DSCP:      &dscp,
		CreatedAt: now,
		UpdatedAt: now,
	}

	updates := vipUpdateValues(vip, VIPRecord{})

	if got := updates["encap_type"]; got != "GRE4" {
		t.Fatalf("encap_type update = %#v, want GRE4", got)
	}
	if got := updates["dscp"]; got != uint8(20) {
		t.Fatalf("dscp update = %#v, want 20", got)
	}
}

func TestUpdateVIPReturnsNotFoundWhenLockedRowMissing(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `vips` WHERE id = \\? ORDER BY `vips`\\.`id` LIMIT \\? FOR UPDATE").
		WithArgs("vip-1", 1).
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
	mock.ExpectRollback()

	ds := &MySQLDataStore{db: db}
	err := ds.UpdateVIP(context.Background(), &models.VIP{
		ID:       "vip-1",
		VIP:      "192.0.2.10",
		Port:     80,
		Protocol: models.ProtocolTCP,
	})
	if !errors.Is(err, datastore.ErrNotFound) {
		t.Fatalf("UpdateVIP error = %v, want ErrNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestUpdateBackendReturnsNotFoundWhenLockedRowMissing(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `backends` WHERE id = \\? ORDER BY `backends`\\.`id` LIMIT \\? FOR UPDATE").
		WithArgs("backend-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"vip_id",
			"ip",
			"weight",
			"created_at",
			"updated_at",
		}))
	mock.ExpectRollback()

	ds := &MySQLDataStore{db: db}
	err := ds.UpdateBackend(context.Background(), &models.Backend{
		ID:     "backend-1",
		VIPID:  "vip-1",
		IP:     "10.0.0.1",
		Weight: 1,
	})
	if !errors.Is(err, datastore.ErrNotFound) {
		t.Fatalf("UpdateBackend error = %v, want ErrNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestIncrementRevisionInTxUsesActualMetadataRowID(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT id, revision FROM system_metadata ORDER BY id LIMIT 1 FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "revision"}).AddRow(7, 41))
	mock.ExpectExec("UPDATE system_metadata SET revision = \\? WHERE id = \\?").
		WithArgs(int64(42), 7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ds := &MySQLDataStore{db: db}
	revision, err := ds.incrementRevisionInTx(db)
	if err != nil {
		t.Fatalf("incrementRevisionInTx returned error: %v", err)
	}
	if revision != 42 {
		t.Fatalf("revision = %d, want 42", revision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestIncrementRevisionInTxFailsWhenMetadataRowNotUpdated(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT id, revision FROM system_metadata ORDER BY id LIMIT 1 FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "revision"}).AddRow(7, 41))
	mock.ExpectExec("UPDATE system_metadata SET revision = \\? WHERE id = \\?").
		WithArgs(int64(42), 7).
		WillReturnResult(sqlmock.NewResult(0, 0))

	ds := &MySQLDataStore{db: db}
	_, err := ds.incrementRevisionInTx(db)
	if err == nil {
		t.Fatal("expected incrementRevisionInTx to fail")
	}
	if !strings.Contains(err.Error(), "metadata row 7 was not updated") {
		t.Fatalf("error = %q, want metadata row not updated", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func newMockGORMDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("failed to open gorm db: %v", err)
	}

	return db, mock, func() {
		_ = sqlDB.Close()
	}
}
