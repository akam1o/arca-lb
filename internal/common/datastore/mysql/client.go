package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Register mysql datastore factory on package init
func init() {
	datastore.Register("mysql", NewMySQLDataStore)
}

// MySQLDataStore implements datastore.DataStore interface using MySQL
type MySQLDataStore struct {
	db *gorm.DB
}

// NewMySQLDataStore creates a new MySQL datastore instance
func NewMySQLDataStore(ctx context.Context, cfg *datastore.Config) (datastore.DataStore, error) {
	// Build DSN with multiStatements enabled for migration execution
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true",
		cfg.MySQLUser,
		cfg.MySQLPassword,
		cfg.MySQLHost,
		cfg.MySQLPort,
		cfg.MySQLDatabase,
	)

	// Open connection
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	// Get underlying sql.DB for connection pool configuration
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	ds := &MySQLDataStore{
		db: db,
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
