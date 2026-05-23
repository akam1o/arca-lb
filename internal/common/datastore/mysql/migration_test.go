package mysql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMigrationStatementsSplitsStatements(t *testing.T) {
	got := migrationStatements(`
CREATE TABLE example (
  id INT NOT NULL,
  name VARCHAR(255) DEFAULT 'semi;colon'
);

INSERT INTO example (id, name) VALUES (1, "quoted;value");
`)
	want := []string{
		"CREATE TABLE example (\n  id INT NOT NULL,\n  name VARCHAR(255) DEFAULT 'semi;colon'\n)",
		`INSERT INTO example (id, name) VALUES (1, "quoted;value")`,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrationStatements() = %#v, want %#v", got, want)
	}
}

func TestApplyMigrationsUsesAdvisoryLock(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT GET_LOCK\\(\\?, \\?\\)").
		WithArgs(mysqlMigrationLockName, mysqlMigrationLockTimeoutSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	mock.ExpectExec("(?s)CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	for _, version := range []string{
		"001_init",
		"002_add_tls_hello_health_check_type",
		"003_align_health_check_fall_count_default",
	} {
		mock.ExpectQuery("SELECT count\\(\\*\\) FROM `schema_migrations` WHERE version = \\?").
			WithArgs(version).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}
	mock.ExpectQuery("SELECT RELEASE_LOCK\\(\\?\\)").
		WithArgs(mysqlMigrationLockName).
		WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))

	ds := &MySQLDataStore{db: db}
	if err := ds.applyMigrations(context.Background()); err != nil {
		t.Fatalf("applyMigrations() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestApplyMigrationsFailsWhenAdvisoryLockTimesOut(t *testing.T) {
	db, mock, cleanup := newMockGORMDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT GET_LOCK\\(\\?, \\?\\)").
		WithArgs(mysqlMigrationLockName, mysqlMigrationLockTimeoutSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(0))

	ds := &MySQLDataStore{db: db}
	err := ds.applyMigrations(context.Background())
	if err == nil {
		t.Fatal("expected applyMigrations() to fail")
	}
	if !strings.Contains(err.Error(), "timed out acquiring migration lock") {
		t.Fatalf("error = %q, want migration lock timeout", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
