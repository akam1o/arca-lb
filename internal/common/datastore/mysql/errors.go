package mysql

import (
	"errors"
	"strings"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"gorm.io/gorm"
)

// normalizeError converts MySQL/GORM errors to datastore errors
func normalizeError(err error) error {
	if err == nil {
		return nil
	}

	// Check for GORM errors
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return datastore.ErrNotFound
	}

	errStr := strings.ToLower(err.Error())

	// Check for MySQL duplicate key error (1062)
	if strings.Contains(errStr, "1062") || strings.Contains(errStr, "duplicate entry") || strings.Contains(errStr, "unique constraint") {
		return datastore.ErrConflict
	}

	// Check for MySQL foreign key constraint error (1452)
	if strings.Contains(errStr, "1452") || strings.Contains(errStr, "foreign key constraint") || strings.Contains(errStr, "cannot add or update a child row") {
		return datastore.ErrInvalidInput
	}

	// Check for MySQL check constraint error (3819)
	if strings.Contains(errStr, "3819") || strings.Contains(errStr, "check constraint") {
		return datastore.ErrInvalidInput
	}

	return err
}

