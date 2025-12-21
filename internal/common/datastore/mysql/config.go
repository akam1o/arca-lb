package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/akam1o/arca-lb/internal/common/models"
	"gorm.io/gorm"
)

// GetConfig retrieves the complete configuration for agent distribution
// Uses a transaction to ensure consistent snapshot
func (ds *MySQLDataStore) GetConfig(ctx context.Context) (*models.Config, error) {
	var config *models.Config

	// Use transaction to ensure consistent snapshot (REPEATABLE READ)
	err := ds.db.Transaction(func(tx *gorm.DB) error {
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

		// Build VIP configs with health checks and backends
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

			// Load health check
			var hcRecord HealthCheckRecord
			if err := tx.Where("vip_id = ?", vipRecord.ID).First(&hcRecord).Error; err == nil {
				var hcConfig models.HCConfig
				if len(hcRecord.Config) > 0 {
					if err := json.Unmarshal(hcRecord.Config, &hcConfig); err != nil {
						return fmt.Errorf("failed to unmarshal health check config for VIP %s: %w", vipRecord.ID, err)
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
				return fmt.Errorf("failed to get health check for VIP %s: %w", vipRecord.ID, err)
			}

			// Load backends
			var backendRecords []BackendRecord
			if err := tx.Where("vip_id = ?", vipRecord.ID).Find(&backendRecords).Error; err != nil {
				return fmt.Errorf("failed to list backends for VIP %s: %w", vipRecord.ID, err)
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

