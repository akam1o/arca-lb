//go:build integration

package mysql

import (
	"context"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/datastore/testsuite"
	"github.com/stretchr/testify/require"
)

func TestMySQLDataStore_CommonTests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	factory := func(ctx context.Context, t *testing.T) (datastore.DataStore, func()) {
		cfg := &datastore.Config{
			Type:          "mysql",
			MySQLHost:     "localhost",
			MySQLPort:     3306,
			MySQLUser:     "arcalb",
			MySQLPassword: "arcalbpass",
			MySQLDatabase: "arcalb_test",
		}

		ds, err := NewMySQLDataStore(ctx, cfg)
		require.NoError(t, err)

		cleanup := func() {
			// Clean up test data
			// Note: In a real scenario, you might want to delete all test data
			ds.Close()
		}

		return ds, cleanup
	}

	testsuite.RunDataStoreTests(t, factory)
}
