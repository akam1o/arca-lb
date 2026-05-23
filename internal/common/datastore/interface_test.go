package datastore_test

import (
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/testutil"
)

func TestMockDataStoreImplementsStoreInterfaces(t *testing.T) {
	mock := testutil.NewMockDataStore()

	var _ datastore.DataStore = mock
	var _ datastore.ControllerStore = mock
	var _ datastore.ConfigSyncStore = mock
}
