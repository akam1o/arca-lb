package mysql

import (
	"context"
	"fmt"

	"github.com/akam1o/arca-lb/internal/common/models"
	"gorm.io/gorm"
)

// GetConfig retrieves the complete configuration for agent distribution
// Uses a transaction to ensure consistent snapshot
func (ds *MySQLDataStore) GetConfig(ctx context.Context) (*models.Config, error) {
	var config *models.Config

	// Use transaction to ensure consistent snapshot (REPEATABLE READ)
	err := ds.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get revision
		var revision int64
		if err := tx.Table("system_metadata").
			Select("revision").
			Limit(1).
			Scan(&revision).Error; err != nil {
			return fmt.Errorf("failed to get revision: %w", err)
		}

		// Get all VIPs
		var vipRecords []VIPRecord
		if err := tx.Find(&vipRecords).Error; err != nil {
			return fmt.Errorf("failed to list VIPs: %w", err)
		}

		config = &models.Config{
			Revision: revision,
			VIPs:     make([]models.VIPConfig, 0, len(vipRecords)),
		}

		vipIDs := make([]string, 0, len(vipRecords))
		for _, vipRecord := range vipRecords {
			vipIDs = append(vipIDs, vipRecord.ID)
		}

		healthChecks, err := loadHealthChecksByVIPID(tx, vipIDs)
		if err != nil {
			return fmt.Errorf("failed to list health checks: %w", err)
		}
		backendsByVIPID, err := loadBackendsByVIPID(tx, vipIDs)
		if err != nil {
			return fmt.Errorf("failed to list backends: %w", err)
		}

		// Build VIP configs with health checks and backends
		for _, vipRecord := range vipRecords {
			vip := vipModelFromRecord(vipRecord)
			if hcRecord, ok := healthChecks[vipRecord.ID]; ok {
				healthCheck, err := healthCheckModelFromRecord(hcRecord)
				if err != nil {
					return fmt.Errorf("failed to unmarshal health check config for VIP %s: %w", vipRecord.ID, err)
				}
				vip.HealthCheck = healthCheck
			}
			backends := backendsByVIPID[vipRecord.ID]
			if backends == nil {
				backends = []models.Backend{}
			}

			vipConfig := models.VIPConfig{
				VIP:         vip,
				HealthCheck: vip.HealthCheck,
				Backends:    backends,
			}
			config.VIPs = append(config.VIPs, vipConfig)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return config, nil
}
