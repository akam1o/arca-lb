package mysql

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// initRevision initializes the revision counter if it doesn't exist
func (ds *MySQLDataStore) initRevision(ctx context.Context) error {
	var count int64
	if err := ds.db.Table("system_metadata").Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check revision: %w", err)
	}

	if count == 0 {
		if err := ds.db.Exec("INSERT INTO system_metadata (revision) VALUES (1)").Error; err != nil {
			return fmt.Errorf("failed to initialize revision: %w", err)
		}
	}

	return nil
}

// GetRevision retrieves the current revision number
func (ds *MySQLDataStore) GetRevision(ctx context.Context) (int64, error) {
	var revision int64
	if err := ds.db.Table("system_metadata").
		Select("revision").
		Limit(1).
		Scan(&revision).Error; err != nil {
		return 0, fmt.Errorf("failed to get revision: %w", err)
	}

	return revision, nil
}

// IncrementRevision atomically increments the revision number
func (ds *MySQLDataStore) IncrementRevision(ctx context.Context) (int64, error) {
	var newRevision int64

	// Use atomic UPDATE to increment revision
	err := ds.db.Transaction(func(tx *gorm.DB) error {
		var err error
		newRevision, err = ds.incrementRevisionInTx(tx)
		return err
	})

	if err != nil {
		return 0, err
	}

	return newRevision, nil
}
