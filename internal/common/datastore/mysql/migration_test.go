package mysql

import (
	"reflect"
	"testing"
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
