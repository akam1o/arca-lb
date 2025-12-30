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

// VIPRecord represents a VIP record in the database
type VIPRecord struct {
	ID        string    `gorm:"primaryKey;type:char(36)"`
	VIP       string    `gorm:"type:varchar(45);not null"`
	Port      int       `gorm:"not null"`
	Protocol  string    `gorm:"type:enum('TCP','UDP');not null"`
	LBMethod  string    `gorm:"type:enum('maglev');not null;default:'maglev'"`
	EncapType *string   `gorm:"type:enum('GRE4','GRE6','L3DSR','NAT4','NAT6');null"`
	DSCP      *uint8    `gorm:"type:tinyint unsigned;null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName returns the table name for VIPRecord
func (VIPRecord) TableName() string {
	return "vips"
}

// HealthCheckRecord represents a health check record in the database
type HealthCheckRecord struct {
	ID          string          `gorm:"primaryKey;type:char(36)"`
	VIPID       string          `gorm:"type:char(36);not null;uniqueIndex"`
	Type        string          `gorm:"type:enum('http','https','tcp','ping');not null"`
	IntervalSec int             `gorm:"not null;default:5"`
	TimeoutSec  int             `gorm:"not null;default:3"`
	RiseCount   int             `gorm:"not null;default:3"`
	FallCount   int             `gorm:"not null;default:3"`
	Config      json.RawMessage `gorm:"type:json"`
	CreatedAt   time.Time       `gorm:"not null"`
	UpdatedAt   time.Time       `gorm:"not null"`
}

// TableName returns the table name for HealthCheckRecord
func (HealthCheckRecord) TableName() string {
	return "health_checks"
}

// CreateVIP creates a new VIP in MySQL
func (ds *MySQLDataStore) CreateVIP(ctx context.Context, vip *models.VIP) error {
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

	// Create VIP in transaction
	err := ds.db.Transaction(func(tx *gorm.DB) error {
		// Create VIP
		if err := tx.Create(&vipRecord).Error; err != nil {
			return normalizeError(fmt.Errorf("failed to create VIP: %w", err))
		}

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

			if err := tx.Create(&hcRecord).Error; err != nil {
				return normalizeError(fmt.Errorf("failed to create health check: %w", err))
			}

			vip.HealthCheck.ID = hcID
			vip.HealthCheck.VIPID = vip.ID
		}

		// Increment revision first
		newRevision, err := ds.incrementRevisionInTx(tx)
		if err != nil {
			return fmt.Errorf("failed to increment revision: %w", err)
		}

		// Log change with the new revision
		if err := ds.logChangeWithRevision(tx, "vip_created", vip.ID, "", newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// GetVIP retrieves a VIP by ID from MySQL
func (ds *MySQLDataStore) GetVIP(ctx context.Context, id string) (*models.VIP, error) {
	var vipRecord VIPRecord
	if err := ds.db.Where("id = ?", id).First(&vipRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, datastore.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get VIP: %w", err)
	}

	// Load health check
	var hcRecord HealthCheckRecord
	hcErr := ds.db.Where("vip_id = ?", id).First(&hcRecord).Error

	vip := &models.VIP{
		ID:        vipRecord.ID,
		VIP:       vipRecord.VIP,
		Port:      vipRecord.Port,
		Protocol:  models.Protocol(vipRecord.Protocol),
		LBMethod:  models.LBMethod(vipRecord.LBMethod),
		CreatedAt: vipRecord.CreatedAt,
		UpdatedAt: vipRecord.UpdatedAt,
	}
	if vipRecord.EncapType != nil {
		vip.EncapType = models.EncapType(*vipRecord.EncapType)
	}
	vip.DSCP = vipRecord.DSCP

	if hcErr == nil {
		var hcConfig models.HCConfig
		if len(hcRecord.Config) > 0 {
			if err := json.Unmarshal(hcRecord.Config, &hcConfig); err != nil {
				// JSON 解析エラーは明示的に返す
				return nil, fmt.Errorf("failed to unmarshal health check config for VIP %s: %w", id, err)
			}
		}

		vip.HealthCheck = &models.HealthCheck{
			ID:          hcRecord.ID,
			VIPID:       hcRecord.VIPID,
			Type:        models.HCType(hcRecord.Type),
			IntervalSec: hcRecord.IntervalSec,
			TimeoutSec:  hcRecord.TimeoutSec,
			RiseCount:   hcRecord.RiseCount,
			FallCount:   hcRecord.FallCount,
			Config:      hcConfig,
			CreatedAt:   hcRecord.CreatedAt,
			UpdatedAt:   hcRecord.UpdatedAt,
		}
	} else if !errors.Is(hcErr, gorm.ErrRecordNotFound) {
		// HealthCheck 取得エラー（not found 以外）はエラーとして返す
		return nil, fmt.Errorf("failed to get health check for VIP %s: %w", id, hcErr)
	}

	return vip, nil
}

// ListVIPs retrieves all VIPs from MySQL
func (ds *MySQLDataStore) ListVIPs(ctx context.Context) ([]models.VIP, error) {
	var vipRecords []VIPRecord
	if err := ds.db.Find(&vipRecords).Error; err != nil {
		return nil, fmt.Errorf("failed to list VIPs: %w", err)
	}

	vips := make([]models.VIP, 0, len(vipRecords))
	for _, vipRecord := range vipRecords {
		vip := models.VIP{
			ID:        vipRecord.ID,
			VIP:       vipRecord.VIP,
			Port:      vipRecord.Port,
			Protocol:  models.Protocol(vipRecord.Protocol),
			LBMethod:  models.LBMethod(vipRecord.LBMethod),
			CreatedAt: vipRecord.CreatedAt,
			UpdatedAt: vipRecord.UpdatedAt,
		}
		if vipRecord.EncapType != nil {
			vip.EncapType = models.EncapType(*vipRecord.EncapType)
		}
		vip.DSCP = vipRecord.DSCP

		// Load health check
		var hcRecord HealthCheckRecord
		if err := ds.db.Where("vip_id = ?", vipRecord.ID).First(&hcRecord).Error; err == nil {
			var hcConfig models.HCConfig
			if len(hcRecord.Config) > 0 {
				if err := json.Unmarshal(hcRecord.Config, &hcConfig); err != nil {
					// JSON 解析エラーは明示的に返す
					return nil, fmt.Errorf("failed to unmarshal health check config for VIP %s: %w", vipRecord.ID, err)
				}
			}

			vip.HealthCheck = &models.HealthCheck{
				ID:          hcRecord.ID,
				VIPID:       hcRecord.VIPID,
				Type:        models.HCType(hcRecord.Type),
				IntervalSec: hcRecord.IntervalSec,
				TimeoutSec:  hcRecord.TimeoutSec,
				RiseCount:   hcRecord.RiseCount,
				FallCount:   hcRecord.FallCount,
				Config:      hcConfig,
				CreatedAt:   hcRecord.CreatedAt,
				UpdatedAt:   hcRecord.UpdatedAt,
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			// HealthCheck 取得エラー（not found 以外）はエラーとして返す
			return nil, fmt.Errorf("failed to get health check for VIP %s: %w", vipRecord.ID, err)
		}

		vips = append(vips, vip)
	}

	return vips, nil
}

// UpdateVIP updates an existing VIP in MySQL
func (ds *MySQLDataStore) UpdateVIP(ctx context.Context, vip *models.VIP) error {
	if vip.ID == "" {
		return fmt.Errorf("VIP ID is required")
	}

	// Check if VIP exists
	existing, err := ds.GetVIP(ctx, vip.ID)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return err
		}
		return fmt.Errorf("failed to get VIP: %w", err)
	}

	// Preserve CreatedAt
	vip.CreatedAt = existing.CreatedAt
	vip.UpdatedAt = time.Now()

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

	// Update VIP in transaction
	err = ds.db.Transaction(func(tx *gorm.DB) error {
		// Update VIP
		result := tx.Model(&VIPRecord{}).Where("id = ?", vip.ID).Updates(&vipRecord)
		if result.Error != nil {
			return normalizeError(fmt.Errorf("failed to update VIP: %w", result.Error))
		}

		// Note: RowsAffected == 0 can occur for idempotent updates (unchanged values)
		// Since we already checked existence above, we don't treat this as ErrNotFound

		// Update or create health check
		if vip.HealthCheck != nil {
			hcConfigJSON, err := json.Marshal(vip.HealthCheck.Config)
			if err != nil {
				return fmt.Errorf("failed to marshal health check config: %w", err)
			}

			// Check if health check already exists
			var existingHC HealthCheckRecord
			err = tx.Where("vip_id = ?", vip.ID).First(&existingHC).Error
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
				if err := tx.Model(&HealthCheckRecord{}).Where("vip_id = ?", vip.ID).Updates(&hcRecord).Error; err != nil {
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
				if err := tx.Create(&hcRecord).Error; err != nil {
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
			if err := tx.Where("vip_id = ?", vip.ID).Delete(&HealthCheckRecord{}).Error; err != nil {
				return normalizeError(fmt.Errorf("failed to delete health check: %w", err))
			}
		}

		// Increment revision
		newRevision, err := ds.incrementRevisionInTx(tx)
		if err != nil {
			return fmt.Errorf("failed to increment revision: %w", err)
		}

		// Log change with the new revision
		if err := ds.logChangeWithRevision(tx, "vip_updated", vip.ID, "", newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// DeleteVIP deletes a VIP and its associated backends from MySQL
func (ds *MySQLDataStore) DeleteVIP(ctx context.Context, id string) error {
	// Delete VIP in transaction (CASCADE will handle backends and health checks)
	err := ds.db.Transaction(func(tx *gorm.DB) error {
		// Delete VIP (CASCADE will delete health checks and backends)
		result := tx.Where("id = ?", id).Delete(&VIPRecord{})
		if result.Error != nil {
			return fmt.Errorf("failed to delete VIP: %w", result.Error)
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
		if err := ds.logChangeWithRevision(tx, "vip_deleted", id, "", newRevision); err != nil {
			return fmt.Errorf("failed to log change: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// incrementRevisionInTx increments revision within a transaction
func (ds *MySQLDataStore) incrementRevisionInTx(tx *gorm.DB) (int64, error) {
	var currentRevision int64
	// Use SELECT ... FOR UPDATE to lock the row
	if err := tx.Raw("SELECT revision FROM system_metadata WHERE id = 1 FOR UPDATE").
		Scan(&currentRevision).Error; err != nil {
		return 0, fmt.Errorf("failed to get current revision: %w", err)
	}

	newRevision := currentRevision + 1
	if err := tx.Exec("UPDATE system_metadata SET revision = ? WHERE id = 1", newRevision).Error; err != nil {
		return 0, fmt.Errorf("failed to increment revision: %w", err)
	}

	return newRevision, nil
}

// logChangeWithRevision logs a change event with a specific revision
func (ds *MySQLDataStore) logChangeWithRevision(tx *gorm.DB, eventType string, vipID, backendID string, revision int64) error {
	changeLog := map[string]interface{}{
		"revision":   revision,
		"event_type": eventType,
	}

	// Set vip_id and backend_id only if they are not empty
	if vipID != "" {
		changeLog["vip_id"] = vipID
	}
	if backendID != "" {
		changeLog["backend_id"] = backendID
	}

	if err := tx.Table("change_log").Create(changeLog).Error; err != nil {
		return fmt.Errorf("failed to log change: %w", err)
	}

	return nil
}
