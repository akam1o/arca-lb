package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/gin-gonic/gin"
)

func TestHandleDataStoreErrorIncludesInvalidInputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handleDataStoreError(c, fmt.Errorf("%w: vip port must be between 1 and 65535", datastore.ErrInvalidInput), "VIP")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if got := w.Body.String(); !strings.Contains(got, "vip port must be between 1 and 65535") {
		t.Fatalf("body = %q, want invalid input details", got)
	}
}
