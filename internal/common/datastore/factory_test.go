package datastore

import (
	"context"
	"strings"
	"testing"
)

func TestNewDataStoreRejectsNilConfig(t *testing.T) {
	_, err := NewDataStore(context.Background(), nil)
	if err == nil {
		t.Fatal("NewDataStore returned nil error for nil config")
	}
	if !strings.Contains(err.Error(), "datastore config is required") {
		t.Fatalf("NewDataStore error = %v, want datastore config error", err)
	}
}
