package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChangeEvent represents a change event to be logged
type ChangeEvent struct {
	EventType string
	VIPID     string
	BackendID string
}

// MySQLTransaction implements datastore.Transaction using MySQL transactions
type MySQLTransaction struct {
	ds      *MySQLDataStore
	tx      *gorm.DB
	ctx     context.Context
	hasOps  bool          // Track if any operations were performed
	changes []ChangeEvent // Track change events for accurate change_log
}

// BeginTx starts a new transaction
func (ds *MySQLDataStore) BeginTx(ctx context.Context) (datastore.Transaction, error) {
	tx := ds.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}

	return &MySQLTransaction{
		ds:      ds,
		tx:      tx,
		ctx:     ctx,
		changes: make([]ChangeEvent, 0),
	}, nil
}

// CreateVIP adds a VIP creation operation to the transaction
func (tx *MySQLTransaction) CreateVIP(ctx context.Context, vip *models.VIP) error {
	db := tx.tx.WithContext(ctx)

	// Generate UUID if not set
	if vip.ID == "" {
		vip.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	if vip.CreatedAt.IsZero() {
		vip.CreatedAt = now
	}
	vip.UpdatedAt = now

	// Set default LB method if not specified
	if vip.LBMethod == "" {
		vip.LBMethod = models.LBMethodMaglev
	}

	// Convert to database record
	var encapType *string
	if vip.EncapType != "" {
		v := string(vip.EncapType)
		encapType = &v
	}
	vipRecord := VIPRecord{
		ID:        vip.ID,
		VIP:       vip.VIP,
		Port:      vip.Port,
		Protocol:  string(vip.Protocol),
		LBMethod:  string(vip.LBMethod),
		EncapType: encapType,
		DSCP:      vip.DSCP,
		CreatedAt: vip.CreatedAt,
		UpdatedAt: vip.UpdatedAt,
	}

	// Create VIP
	if err := db.Create(&vipRecord).Error; err != nil {
		return normalizeError(fmt.Errorf("failed to create VIP: %w", err))
	}

	tx.hasOps = true

	// Create health check if provided
	if vip.HealthCheck != nil {
		hcConfigJSON, err := json.Marshal(vip.HealthCheck.Config)
		if err != nil {
			return fmt.Errorf("failed to marshal health check config: %w", err)
		}

		hcID := vip.HealthCheck.ID
		if hcID == "" {
			hcID = uuid.New().String()
		}

		hcRecord := HealthCheckRecord{
			ID:          hcID,
			VIPID:       vip.ID,
			Type:        string(vip.HealthCheck.Type),
			IntervalSec: vip.HealthCheck.IntervalSec,
			TimeoutSec:  vip.HealthCheck.TimeoutSec,
			RiseCount:   vip.HealthCheck.RiseCount,
			FallCount:   vip.HealthCheck.FallCount,
			Config:      hcConfigJSON,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := db.Create(&hcRecord).Error; err != nil {
			return normalizeError(fmt.Errorf("failed to create health check: %w", err))
		}

		vip.HealthCheck.ID = hcID
		vip.HealthCheck.VIPID = vip.ID
	}

	return nil
}

// AddBackend adds a backend creation operation to the transaction
func (tx *MySQLTransaction) AddBackend(ctx context.Context, backend *models.Backend) error {
	db := tx.tx.WithContext(ctx)

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

	// Verify VIP exists
	var count int64
	if err := db.Table("vips").Where("id = ?", backend.VIPID).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to verify VIP: %w", err)
	}
	if count == 0 {
		return datastore.ErrNotFound
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

	// Create backend
	if err := db.Create(&backendRecord).Error; err != nil {
		return normalizeError(fmt.Errorf("failed to create backend: %w", err))
	}

	tx.hasOps = true
	tx.changes = append(tx.changes, ChangeEvent{
		EventType: "backend_added",
		VIPID:     backend.VIPID,
		BackendID: backend.ID,
	})
	return nil
}

// UpdateVIP adds a VIP update operation to the transaction
func (tx *MySQLTransaction) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	db := tx.tx.WithContext(ctx)

	if vip.ID == "" {
		return fmt.Errorf("VIP ID is required")
	}

	// Check if VIP exists
	var existingRecord VIPRecord
	if err := db.Where("id = ?", vip.ID).First(&existingRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return datastore.ErrNotFound
		}
		return normalizeError(fmt.Errorf("failed to check VIP: %w", err))
	}

	vip.UpdatedAt = time.Now()
	vip.CreatedAt = existingRecord.CreatedAt

	// Update VIP
	result := db.Model(&VIPRecord{}).
		Where("id = ?", vip.ID).
		Updates(vipUpdateValues(vip, existingRecord))
	if result.Error != nil {
		return normalizeError(fmt.Errorf("failed to update VIP: %w", result.Error))
	}

	// Note: RowsAffected == 0 can occur for idempotent updates (unchanged values)
	// Since we already checked existence above, we don't treat this as ErrNotFound

	// Update or create health check if provided
	if vip.HealthCheck != nil {
		hcConfigJSON, err := json.Marshal(vip.HealthCheck.Config)
		if err != nil {
			return fmt.Errorf("failed to marshal health check config: %w", err)
		}

		// Check if health check already exists
		var existingHC HealthCheckRecord
		err = db.Where("vip_id = ?", vip.ID).First(&existingHC).Error
		if err == nil {
			// Update existing - use existing ID
			hcRecord := HealthCheckRecord{
				ID:          existingHC.ID,
				VIPID:       vip.ID,
				Type:        string(vip.HealthCheck.Type),
				IntervalSec: vip.HealthCheck.IntervalSec,
				TimeoutSec:  vip.HealthCheck.TimeoutSec,
				RiseCount:   vip.HealthCheck.RiseCount,
				FallCount:   vip.HealthCheck.FallCount,
				Config:      hcConfigJSON,
				CreatedAt:   existingHC.CreatedAt,
				UpdatedAt:   time.Now(),
			}
			if err := db.Model(&HealthCheckRecord{}).Where("vip_id = ?", vip.ID).Updates(&hcRecord).Error; err != nil {
				return normalizeError(fmt.Errorf("failed to update health check: %w", err))
			}
			vip.HealthCheck.ID = existingHC.ID
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			// Create new
			hcID := vip.HealthCheck.ID
			if hcID == "" {
				hcID = uuid.New().String()
			}
			hcRecord := HealthCheckRecord{
				ID:          hcID,
				VIPID:       vip.ID,
				Type:        string(vip.HealthCheck.Type),
				IntervalSec: vip.HealthCheck.IntervalSec,
				TimeoutSec:  vip.HealthCheck.TimeoutSec,
				RiseCount:   vip.HealthCheck.RiseCount,
				FallCount:   vip.HealthCheck.FallCount,
				Config:      hcConfigJSON,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := db.Create(&hcRecord).Error; err != nil {
				return normalizeError(fmt.Errorf("failed to create health check: %w", err))
			}
			vip.HealthCheck.ID = hcID
		} else {
			// Other errors (DB failure, etc.)
			return normalizeError(fmt.Errorf("failed to check health check: %w", err))
		}

		vip.HealthCheck.VIPID = vip.ID
	} else {
		// Delete existing health check if vip.HealthCheck is nil
		if err := db.Where("vip_id = ?", vip.ID).Delete(&HealthCheckRecord{}).Error; err != nil {
			return normalizeError(fmt.Errorf("failed to delete health check: %w", err))
		}
	}

	tx.hasOps = true
	tx.changes = append(tx.changes, ChangeEvent{
		EventType: "vip_updated",
		VIPID:     vip.ID,
		BackendID: "",
	})
	return nil
}

// DeleteVIP adds a VIP deletion operation to the transaction
func (tx *MySQLTransaction) DeleteVIP(ctx context.Context, id string) error {
	db := tx.tx.WithContext(ctx)

	// Delete VIP (CASCADE will delete health checks and backends)
	result := db.Where("id = ?", id).Delete(&VIPRecord{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete VIP: %w", result.Error)
	}

	// Check if any row was affected
	if result.RowsAffected == 0 {
		return datastore.ErrNotFound
	}

	tx.hasOps = true
	tx.changes = append(tx.changes, ChangeEvent{
		EventType: "vip_deleted",
		VIPID:     id,
		BackendID: "",
	})
	return nil
}

// Commit commits the transaction
func (tx *MySQLTransaction) Commit() error {
	db := tx.tx.WithContext(tx.ctx)

	// Only increment revision and log change if operations were performed
	if !tx.hasOps {
		// No operations, just commit
		if err := db.Commit().Error; err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		return nil
	}

	// Increment revision first
	newRevision, err := tx.ds.incrementRevisionInTx(db)
	if err != nil {
		tx.tx.Rollback()
		return fmt.Errorf("failed to increment revision: %w", err)
	}

	// Log all change events with the new revision
	for _, change := range tx.changes {
		if err := tx.ds.logChangeWithRevision(db, change.EventType, change.VIPID, change.BackendID, newRevision); err != nil {
			tx.tx.Rollback()
			return fmt.Errorf("failed to log change: %w", err)
		}
	}

	// Commit transaction
	if err := db.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Rollback rolls back the transaction
func (tx *MySQLTransaction) Rollback() error {
	if err := tx.tx.WithContext(tx.ctx).Rollback().Error; err != nil {
		return fmt.Errorf("failed to rollback transaction: %w", err)
	}
	return nil
}
