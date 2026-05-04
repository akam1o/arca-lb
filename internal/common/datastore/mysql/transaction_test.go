package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/akam1o/arca-lb/internal/common/models"
)

func TestMySQLTransactionCreateVIPRecordsChangeEvent(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectBegin()
	gormTx := db.Begin()
	if gormTx.Error != nil {
		t.Fatalf("begin transaction: %v", gormTx.Error)
	}

	mock.ExpectExec("INSERT INTO `vips`").
		WillReturnResult(sqlmock.NewResult(1, 1))

	tx := &MySQLTransaction{
		tx:      gormTx,
		changes: make([]ChangeEvent, 0),
	}
	vip := &models.VIP{
		VIP:      "192.0.2.10",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	if err := tx.CreateVIP(context.Background(), vip); err != nil {
		t.Fatalf("CreateVIP returned error: %v", err)
	}
	if !tx.hasOps {
		t.Fatal("CreateVIP did not mark the transaction as having operations")
	}
	if len(tx.changes) != 1 {
		t.Fatalf("change event count = %d, want 1", len(tx.changes))
	}
	change := tx.changes[0]
	if change.EventType != "vip_created" || change.VIPID != vip.ID || change.BackendID != "" {
		t.Fatalf("change event = %#v, want vip_created for VIP %s", change, vip.ID)
	}

	mock.ExpectRollback()
	if err := gormTx.Rollback().Error; err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
