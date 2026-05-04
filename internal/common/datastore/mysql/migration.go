package mysql

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// applyMigrations applies all migration files in order
func (ds *MySQLDataStore) applyMigrations(ctx context.Context) error {
	db := ds.db.WithContext(ctx)

	// Create schema_migrations table if not exists
	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
	`).Error; err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Read migration files
	// Note: embed path is ../../../../migrations/*.sql, so we need to match "migrations/*.sql"
	migrations, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("failed to read migration files: %w", err)
	}

	// Sort migrations by filename
	sort.Strings(migrations)

	// Apply each migration
	for _, migrationFile := range migrations {
		version := filepath.Base(migrationFile)
		version = strings.TrimSuffix(version, ".sql")

		// Check if migration already applied
		var count int64
		if err := db.Table("schema_migrations").Where("version = ?", version).Count(&count).Error; err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}

		if count > 0 {
			continue // Migration already applied
		}

		// Read migration file
		content, err := migrationsFS.ReadFile(migrationFile)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", migrationFile, err)
		}

		// Execute migration
		if err := db.Exec(string(content)).Error; err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", version, err)
		}

		// Record migration
		if err := db.Table("schema_migrations").Create(map[string]interface{}{
			"version": version,
		}).Error; err != nil {
			return fmt.Errorf("failed to record migration %s: %w", version, err)
		}
	}

	return nil
}
