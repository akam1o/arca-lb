package mysql

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	dsn, err := mysqlDSN(cfg)
	if err != nil {
		t.Fatalf("mysqlDSN: %v", err)
	}
	parsed, err := drivermysql.ParseDSN(dsn)
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

func TestMySQLDSNIncludesTLSMode(t *testing.T) {
	cfg := &datastore.Config{
		MySQLUser:     "arcalb",
		MySQLPassword: "secret",
		MySQLHost:     "db.example.com",
		MySQLPort:     3306,
		MySQLDatabase: "arcalb",
		MySQLTLSMode:  "true",
	}

	dsn, err := mysqlDSN(cfg)
	if err != nil {
		t.Fatalf("mysqlDSN: %v", err)
	}
	parsed, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}

	if parsed.TLSConfig != "true" {
		t.Fatalf("tls config = %q, want true", parsed.TLSConfig)
	}
}

func TestMySQLDSNRegistersCustomTLSConfig(t *testing.T) {
	caFile := writeTestCACert(t)
	cfg := &datastore.Config{
		MySQLUser:          "arcalb",
		MySQLPassword:      "secret",
		MySQLHost:          "10.0.0.10",
		MySQLPort:          3306,
		MySQLDatabase:      "arcalb",
		MySQLTLSMode:       "custom",
		MySQLTLSCAFile:     caFile,
		MySQLTLSServerName: "mysql.internal.example",
	}

	dsn, err := mysqlDSN(cfg)
	if err != nil {
		t.Fatalf("mysqlDSN: %v", err)
	}
	parsed, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	defer drivermysql.DeregisterTLSConfig(parsed.TLSConfig)

	if parsed.TLSConfig == "" {
		t.Fatal("tls config is empty, want custom registered config")
	}
	if parsed.TLS == nil {
		t.Fatal("TLS config = nil, want registered TLS config clone")
	}
	if parsed.TLS.ServerName != cfg.MySQLTLSServerName {
		t.Fatalf("TLS ServerName = %q, want %q", parsed.TLS.ServerName, cfg.MySQLTLSServerName)
	}
	if parsed.TLS.RootCAs == nil {
		t.Fatal("TLS RootCAs = nil, want custom CA pool")
	}
}

func TestMySQLConnectionSettingsFromConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *datastore.Config
		want mysqlConnectionSettings
	}{
		{
			name: "defaults",
			cfg:  &datastore.Config{},
			want: mysqlConnectionSettings{
				maxOpenConns:      DefaultMaxOpenConns,
				maxIdleConns:      DefaultMaxIdleConns,
				connMaxLifetime:   DefaultConnMaxLifetime,
				watchPollInterval: DefaultWatchPollInterval,
			},
		},
		{
			name: "configured",
			cfg: &datastore.Config{
				MySQLMaxOpenConns:      50,
				MySQLMaxIdleConns:      12,
				MySQLConnMaxLifetime:   10 * time.Minute,
				MySQLWatchPollInterval: 250 * time.Millisecond,
			},
			want: mysqlConnectionSettings{
				maxOpenConns:      50,
				maxIdleConns:      12,
				connMaxLifetime:   10 * time.Minute,
				watchPollInterval: 250 * time.Millisecond,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mysqlConnectionSettingsFromConfig(tt.cfg)
			if got != tt.want {
				t.Fatalf("settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func writeTestCACert(t *testing.T) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}
