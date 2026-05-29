package api

import (
	"errors"
	"net/http"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/gin-gonic/gin"
)

// handleDataStoreError handles datastore errors and returns appropriate HTTP status
func handleDataStoreError(c *gin.Context, err error, resource string) {
	if errors.Is(err, datastore.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": resource + " not found"})
	} else if errors.Is(err, datastore.ErrConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": resource + " already exists"})
	} else if errors.Is(err, datastore.ErrInvalidInput) {
		c.JSON(http.StatusBadRequest, gin.H{"error": invalidInputMessage(err)})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func invalidInputMessage(err error) string {
	if err == nil || err.Error() == datastore.ErrInvalidInput.Error() {
		return "invalid input"
	}
	return err.Error()
}
