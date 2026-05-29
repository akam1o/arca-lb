package mysql

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	mysqlMigrationLockName           = "arca_lb_schema_migrations"
	mysqlMigrationLockTimeoutSeconds = 60
)

// applyMigrations applies all migration files in order
func (ds *MySQLDataStore) applyMigrations(ctx context.Context) error {
	return ds.db.WithContext(ctx).Connection(func(db *gorm.DB) (err error) {
		if err := acquireMigrationLock(db); err != nil {
			return err
		}
		defer func() {
			if releaseErr := releaseMigrationLock(db); releaseErr != nil {
				if err != nil {
					err = fmt.Errorf("%w; failed to release migration lock: %v", err, releaseErr)
					return
				}
				err = releaseErr
			}
		}()

		return applyMigrationsLocked(db)
	})
}

func acquireMigrationLock(db *gorm.DB) error {
	var locked sql.NullInt64
	if err := migrationSession(db).Raw("SELECT GET_LOCK(?, ?)", mysqlMigrationLockName, mysqlMigrationLockTimeoutSeconds).Scan(&locked).Error; err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}
	if !locked.Valid {
		return fmt.Errorf("failed to acquire migration lock %q: result was NULL", mysqlMigrationLockName)
	}
	if locked.Int64 != 1 {
		return fmt.Errorf("timed out acquiring migration lock %q", mysqlMigrationLockName)
	}
	return nil
}

func releaseMigrationLock(db *gorm.DB) error {
	var released sql.NullInt64
	if err := migrationSession(db).Raw("SELECT RELEASE_LOCK(?)", mysqlMigrationLockName).Scan(&released).Error; err != nil {
		return fmt.Errorf("failed to release migration lock: %w", err)
	}
	if !released.Valid {
		return fmt.Errorf("failed to release migration lock %q: result was NULL", mysqlMigrationLockName)
	}
	if released.Int64 != 1 {
		return fmt.Errorf("migration lock %q was not held by this connection", mysqlMigrationLockName)
	}
	return nil
}

func applyMigrationsLocked(db *gorm.DB) error {
	// Create schema_migrations table if not exists
	if err := migrationSession(db).Exec(`
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
		if err := migrationSession(db).Table("schema_migrations").Where("version = ?", version).Count(&count).Error; err != nil {
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

		// Execute migration statements individually so the runtime DSN does not
		// need multiStatements enabled.
		for _, statement := range migrationStatements(string(content)) {
			if err := migrationSession(db).Exec(statement).Error; err != nil {
				return fmt.Errorf("failed to apply migration %s: %w", version, err)
			}
		}

		// Record migration
		if err := migrationSession(db).Table("schema_migrations").Create(map[string]interface{}{
			"version": version,
		}).Error; err != nil {
			return fmt.Errorf("failed to record migration %s: %w", version, err)
		}
	}

	return nil
}

func migrationSession(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true})
}

func migrationStatements(sql string) []string {
	statements := make([]string, 0)
	start := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false

	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch ch {
		case '\'':
			if !inDoubleQuote && !inBacktick {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
		case '`':
			if !inSingleQuote && !inDoubleQuote {
				inBacktick = !inBacktick
			}
		case ';':
			if inSingleQuote || inDoubleQuote || inBacktick {
				continue
			}
			statement := strings.TrimSpace(sql[start:i])
			if statement != "" {
				statements = append(statements, statement)
			}
			start = i + 1
		}
	}

	statement := strings.TrimSpace(sql[start:])
	if statement != "" {
		statements = append(statements, statement)
	}
	return statements
}
