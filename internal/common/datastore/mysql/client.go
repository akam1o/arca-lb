package mysql

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	drivermysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	// DefaultConnectTimeout bounds the initial MySQL TCP handshake.
	DefaultConnectTimeout = 5 * time.Second

	// DefaultReadTimeout bounds MySQL socket reads.
	DefaultReadTimeout = 10 * time.Second

	// DefaultWriteTimeout bounds MySQL socket writes.
	DefaultWriteTimeout = 10 * time.Second

	// DefaultMaxOpenConns preserves the historical MySQL connection pool size.
	DefaultMaxOpenConns = 25

	// DefaultMaxIdleConns preserves the historical idle MySQL connection pool size.
	DefaultMaxIdleConns = 5

	// DefaultConnMaxLifetime preserves the historical MySQL connection lifetime.
	DefaultConnMaxLifetime = 5 * time.Minute

	// DefaultWatchPollInterval preserves the historical MySQL watch polling interval.
	DefaultWatchPollInterval = 100 * time.Millisecond
)

// Register mysql datastore factory on package init
func init() {
	datastore.Register("mysql", NewMySQLDataStore)
}

// MySQLDataStore implements datastore.DataStore interface using MySQL
type MySQLDataStore struct {
	db                *gorm.DB
	watchPollInterval time.Duration
}

// NewMySQLDataStore creates a new MySQL datastore instance
func NewMySQLDataStore(ctx context.Context, cfg *datastore.Config) (datastore.DataStore, error) {
	dsn, err := mysqlDSN(cfg)
	if err != nil {
		return nil, err
	}
	connectionSettings := mysqlConnectionSettingsFromConfig(cfg)

	// Open connection
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	// Get underlying sql.DB for connection pool configuration
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(connectionSettings.maxOpenConns)
	sqlDB.SetMaxIdleConns(connectionSettings.maxIdleConns)
	sqlDB.SetConnMaxLifetime(connectionSettings.connMaxLifetime)

	// Test connection
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	ds := &MySQLDataStore{
		db:                db,
		watchPollInterval: connectionSettings.watchPollInterval,
	}

	// Apply migrations
	if err := ds.applyMigrations(ctx); err != nil {
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	// Initialize revision if not exists
	if err := ds.initRevision(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize revision: %w", err)
	}

	return ds, nil
}

func mysqlDSN(cfg *datastore.Config) (string, error) {
	dsn := drivermysql.NewConfig()
	dsn.User = cfg.MySQLUser
	dsn.Passwd = cfg.MySQLPassword
	dsn.Net = "tcp"
	dsn.Addr = fmt.Sprintf("%s:%d", cfg.MySQLHost, cfg.MySQLPort)
	dsn.DBName = cfg.MySQLDatabase
	tlsMode, err := mysqlTLSMode(cfg)
	if err != nil {
		return "", err
	}
	dsn.TLSConfig = tlsMode
	dsn.Timeout = DefaultConnectTimeout
	dsn.ReadTimeout = DefaultReadTimeout
	dsn.WriteTimeout = DefaultWriteTimeout
	dsn.Params = map[string]string{
		"charset":   "utf8mb4",
		"parseTime": "True",
		"loc":       "Local",
	}
	return dsn.FormatDSN(), nil
}

func mysqlTLSMode(cfg *datastore.Config) (string, error) {
	switch strings.ToLower(cfg.MySQLTLSMode) {
	case "":
		return "", nil
	case "false", "true", "skip-verify", "preferred":
		return strings.ToLower(cfg.MySQLTLSMode), nil
	case "custom":
		return registerMySQLCustomTLSConfig(cfg)
	default:
		return "", fmt.Errorf("invalid MySQL TLS mode: %s", cfg.MySQLTLSMode)
	}
}

func registerMySQLCustomTLSConfig(cfg *datastore.Config) (string, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if cfg.MySQLTLSServerName != "" {
		tlsConfig.ServerName = cfg.MySQLTLSServerName
	} else {
		tlsConfig.ServerName = cfg.MySQLHost
	}
	if cfg.MySQLTLSCAFile != "" {
		rootCAs, err := rootCAPool(cfg.MySQLTLSCAFile)
		if err != nil {
			return "", err
		}
		tlsConfig.RootCAs = rootCAs
	}
	if cfg.MySQLTLSCertFile != "" || cfg.MySQLTLSKeyFile != "" {
		if cfg.MySQLTLSCertFile == "" || cfg.MySQLTLSKeyFile == "" {
			return "", fmt.Errorf("MySQL TLS client certificate and key must both be set")
		}
		certificate, err := tls.LoadX509KeyPair(cfg.MySQLTLSCertFile, cfg.MySQLTLSKeyFile)
		if err != nil {
			return "", fmt.Errorf("failed to load MySQL TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}

	name := mysqlCustomTLSConfigName(cfg)
	if err := drivermysql.RegisterTLSConfig(name, tlsConfig); err != nil {
		return "", fmt.Errorf("failed to register MySQL TLS config: %w", err)
	}
	return name, nil
}

func rootCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read MySQL TLS CA file: %w", err)
	}
	rootCAs := x509.NewCertPool()
	if ok := rootCAs.AppendCertsFromPEM(pem); !ok {
		return nil, fmt.Errorf("failed to parse MySQL TLS CA file")
	}
	return rootCAs, nil
}

func mysqlCustomTLSConfigName(cfg *datastore.Config) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		cfg.MySQLHost,
		cfg.MySQLTLSServerName,
		cfg.MySQLTLSCAFile,
		cfg.MySQLTLSCertFile,
		cfg.MySQLTLSKeyFile,
	}, "\x00")))
	return fmt.Sprintf("arca-lb-%x", sum[:8])
}

type mysqlConnectionSettings struct {
	maxOpenConns      int
	maxIdleConns      int
	connMaxLifetime   time.Duration
	watchPollInterval time.Duration
}

func mysqlConnectionSettingsFromConfig(cfg *datastore.Config) mysqlConnectionSettings {
	settings := mysqlConnectionSettings{
		maxOpenConns:      DefaultMaxOpenConns,
		maxIdleConns:      DefaultMaxIdleConns,
		connMaxLifetime:   DefaultConnMaxLifetime,
		watchPollInterval: DefaultWatchPollInterval,
	}
	if cfg.MySQLMaxOpenConns > 0 {
		settings.maxOpenConns = cfg.MySQLMaxOpenConns
	}
	if cfg.MySQLMaxIdleConns > 0 {
		settings.maxIdleConns = cfg.MySQLMaxIdleConns
	}
	if cfg.MySQLConnMaxLifetime > 0 {
		settings.connMaxLifetime = cfg.MySQLConnMaxLifetime
	}
	if cfg.MySQLWatchPollInterval > 0 {
		settings.watchPollInterval = cfg.MySQLWatchPollInterval
	}
	return settings
}

// Close closes the MySQL connection
func (ds *MySQLDataStore) Close() error {
	if ds.db != nil {
		sqlDB, err := ds.db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}
