package mysql

import (
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	drivermysql "github.com/go-sql-driver/mysql"
)

func TestMySQLDSNEscapesConnectionFields(t *testing.T) {
	cfg := &datastore.Config{
		MySQLUser:     "user@name",
		MySQLPassword: "p@ss/word?x=%",
		MySQLHost:     "db.example.com",
		MySQLPort:     3307,
		MySQLDatabase: "arca/lb?prod",
	}

	parsed, err := drivermysql.ParseDSN(mysqlDSN(cfg))
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}

	if parsed.User != cfg.MySQLUser {
		t.Fatalf("user = %q, want %q", parsed.User, cfg.MySQLUser)
	}
	if parsed.Passwd != cfg.MySQLPassword {
		t.Fatalf("password = %q, want %q", parsed.Passwd, cfg.MySQLPassword)
	}
	if parsed.Addr != "db.example.com:3307" {
		t.Fatalf("addr = %q, want db.example.com:3307", parsed.Addr)
	}
	if parsed.DBName != cfg.MySQLDatabase {
		t.Fatalf("database = %q, want %q", parsed.DBName, cfg.MySQLDatabase)
	}
	if parsed.MultiStatements {
		t.Fatal("multiStatements = true, want false")
	}
	if !parsed.ParseTime {
		t.Fatal("parseTime = false, want true")
	}
	if parsed.Loc == nil || parsed.Loc.String() != "Local" {
		t.Fatalf("loc = %v, want Local", parsed.Loc)
	}
	if parsed.Timeout != DefaultConnectTimeout {
		t.Fatalf("timeout = %s, want %s", parsed.Timeout, DefaultConnectTimeout)
	}
	if parsed.ReadTimeout != DefaultReadTimeout {
		t.Fatalf("readTimeout = %s, want %s", parsed.ReadTimeout, DefaultReadTimeout)
	}
	if parsed.WriteTimeout != DefaultWriteTimeout {
		t.Fatalf("writeTimeout = %s, want %s", parsed.WriteTimeout, DefaultWriteTimeout)
	}
}
