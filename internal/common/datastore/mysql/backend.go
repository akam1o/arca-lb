package mysql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BackendRecord represents a backend record in the database
type BackendRecord struct {
	ID        string    `gorm:"primaryKey;type:char(36)"`
	VIPID     string    `gorm:"column:vip_id;type:char(36);not null;index"`
	IP        string    `gorm:"type:varchar(45);not null"`
	Weight    int       `gorm:"not null;default:1"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName returns the table name for BackendRecord
func (BackendRecord) TableName() string {
	return "backends"
}

func backendModelFromRecord(backendRecord BackendRecord) models.Backend {
	return models.Backend{
		ID:        backendRecord.ID,
		VIPID:     backendRecord.VIPID,
		IP:        backendRecord.IP,
		Weight:    backendRecord.Weight,
		CreatedAt: backendRecord.CreatedAt,
		UpdatedAt: backendRecord.UpdatedAt,
	}
}

func loadBackendsByVIPID(db *gorm.DB, vipIDs []string) (map[string][]models.Backend, error) {
	backends := make(map[string][]models.Backend, len(vipIDs))
	if len(vipIDs) == 0 {
		return backends, nil
	}

	var backendRecords []BackendRecord
	if err := db.Where("vip_id IN ?", vipIDs).Find(&backendRecords).Error; err != nil {
		return nil, err
	}
	for _, backendRecord := range backendRecords {
		backend := backendModelFromRecord(backendRecord)
		backends[backend.VIPID] = append(backends[backend.VIPID], backend)
	}
	return backends, nil
}

// AddBackend adds a new backend to MySQL
func (ds *MySQLDataStore) AddBackend(ctx context.Context, backend *models.Backend) error {
	if backend == nil {
		return datastore.ErrInvalidInput
	}
	if err := datastore.ValidateBackendForWrite(backend); err != nil {
		return err
	}

	// Generate UUID if not set
	if backend.ID == "" {
		backend.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if backend.CreatedAt.IsZero() {
		backend.CreatedAt = now
	}
	backend.UpdatedAt = now

	// Convert to database record
	backendRecord := BackendRecord{
		ID:        backend.ID,
		VIPID:     backend.VIPID,
		IP:        backend.IP,
		Weight:    backend.Weight,
		CreatedAt: backend.CreatedAt,
		UpdatedAt: backend.UpdatedAt,
	}

	// Add backend in transaction
	err := ds.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		vipRecord, err := lockVIPRecordForUpdate(tx, backend.VIPID)
		if err != nil {
			return err
		}
		vip := vipModelFromRecord(vipRecord)
		if err := datastore.ValidateBackendAddressFamilyForVIP(&vip, backend); err != nil {
			return err
		}

		// Create backend
		if err := tx.Create(&backendRecord).Error; err != nil {
			return normalizeError(fmt.Errorf("failed to create backend: %w", err))
		}

		// Increment revision
		newRevision, err := ds.incrementRevisionInTx(tx)
		if err != nil {
			return fmt.Errorf("failed to increment revision: %w", err)
		}

		// Log change with the new revision
		if err := ds.logChangeWithRevision(tx, "backend_added", backend.VIPID, backend.ID, newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// GetBackend retrieves a backend by ID from MySQL
func (ds *MySQLDataStore) GetBackend(ctx context.Context, id string) (*models.Backend, error) {
	if err := datastore.ValidateResourceID("backend id", id); err != nil {
		return nil, err
	}

	db := ds.db.WithContext(ctx)

	var backendRecord BackendRecord
	if err := db.Where("id = ?", id).First(&backendRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, datastore.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get backend: %w", err)
	}

	return &models.Backend{
		ID:        backendRecord.ID,
		VIPID:     backendRecord.VIPID,
		IP:        backendRecord.IP,
		Weight:    backendRecord.Weight,
		CreatedAt: backendRecord.CreatedAt,
		UpdatedAt: backendRecord.UpdatedAt,
	}, nil
}

// ListBackends retrieves all backends for a VIP from MySQL
func (ds *MySQLDataStore) ListBackends(ctx context.Context, vipID string) ([]models.Backend, error) {
	if err := datastore.ValidateResourceID("backend vip_id", vipID); err != nil {
		return nil, err
	}

	db := ds.db.WithContext(ctx)

	var backendRecords []BackendRecord
	if err := db.Where("vip_id = ?", vipID).Find(&backendRecords).Error; err != nil {
		return nil, fmt.Errorf("failed to list backends: %w", err)
	}

	backends := make([]models.Backend, 0, len(backendRecords))
	for _, backendRecord := range backendRecords {
		backends = append(backends, models.Backend{
			ID:        backendRecord.ID,
			VIPID:     backendRecord.VIPID,
			IP:        backendRecord.IP,
			Weight:    backendRecord.Weight,
			CreatedAt: backendRecord.CreatedAt,
			UpdatedAt: backendRecord.UpdatedAt,
		})
	}

	return backends, nil
}

// UpdateBackend updates an existing backend in MySQL
func (ds *MySQLDataStore) UpdateBackend(ctx context.Context, backend *models.Backend) error {
	if backend == nil {
		return datastore.ErrInvalidInput
	}

	if err := datastore.ValidateResourceID("backend id", backend.ID); err != nil {
		return err
	}
	if err := datastore.ValidateBackendFieldsForWrite(backend); err != nil {
		return err
	}

	// Update backend in transaction
	err := ds.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing BackendRecord
		if err := tx.Where("id = ?", backend.ID).
			First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return datastore.ErrNotFound
			}
			return fmt.Errorf("failed to get backend: %w", err)
		}

		vipRecord, err := lockVIPRecordForUpdate(tx, existing.VIPID)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", backend.ID).
			First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return datastore.ErrNotFound
			}
			return fmt.Errorf("failed to get backend: %w", err)
		}

		// Preserve CreatedAt and VIPID from the locked row.
		backend.CreatedAt = existing.CreatedAt
		backend.VIPID = existing.VIPID
		backend.UpdatedAt = time.Now()
		if err := datastore.ValidateBackendForWrite(backend); err != nil {
			return err
		}
		vip := vipModelFromRecord(vipRecord)
		if err := datastore.ValidateBackendAddressFamilyForVIP(&vip, backend); err != nil {
			return err
		}

		// Convert to database record
		backendRecord := BackendRecord{
			ID:        backend.ID,
			VIPID:     backend.VIPID,
			IP:        backend.IP,
			Weight:    backend.Weight,
			CreatedAt: backend.CreatedAt,
			UpdatedAt: backend.UpdatedAt,
		}

		// Update backend
		result := tx.Model(&BackendRecord{}).Where("id = ?", backend.ID).Updates(&backendRecord)
		if result.Error != nil {
			return normalizeError(fmt.Errorf("failed to update backend: %w", result.Error))
		}

		// Note: RowsAffected == 0 can occur for idempotent updates (unchanged values)
		// Since we already checked existence above, we don't treat this as ErrNotFound

		// Increment revision
		newRevision, err := ds.incrementRevisionInTx(tx)
		if err != nil {
			return fmt.Errorf("failed to increment revision: %w", err)
		}

		// Log change with the new revision
		if err := ds.logChangeWithRevision(tx, "backend_updated", backend.VIPID, backend.ID, newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// DeleteBackend deletes a backend from MySQL
func (ds *MySQLDataStore) DeleteBackend(ctx context.Context, id string) error {
	if err := datastore.ValidateResourceID("backend id", id); err != nil {
		return err
	}

	// Get backend to find VIP ID
	backend, err := ds.GetBackend(ctx, id)
	if err != nil {
		return err
	}

	// Delete backend in transaction
	err = ds.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete backend
		result := tx.Where("id = ?", id).Delete(&BackendRecord{})
		if result.Error != nil {
			return fmt.Errorf("failed to delete backend: %w", result.Error)
		}

		// Check if any row was affected
		if result.RowsAffected == 0 {
			return datastore.ErrNotFound
		}

		// Increment revision
		newRevision, err := ds.incrementRevisionInTx(tx)
		if err != nil {
			return fmt.Errorf("failed to increment revision: %w", err)
		}

		// Log change with the new revision
		if err := ds.logChangeWithRevision(tx, "backend_deleted", backend.VIPID, id, newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
