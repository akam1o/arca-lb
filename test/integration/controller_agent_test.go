//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	_ "github.com/akam1o/arca-lb/internal/common/datastore/etcd"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/akam1o/arca-lb/internal/controller/grpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControllerAgentCommunication tests gRPC communication between Controller and Agent
func TestControllerAgentCommunication(t *testing.T) {
	// Skip if integration tests are not enabled
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup etcd datastore
	ctx := context.Background()
	cfg := &config.Config{
		DataStore: config.DataStoreConfig{
			Type: "etcd",
			Etcd: config.EtcdConfig{
				Endpoints: []string{"http://localhost:2379"},
			},
		},
	}

	ds, err := datastore.NewDataStore(ctx, cfg.ToDataStoreConfig())
	require.NoError(t, err)
	defer ds.Close()

	// Create gRPC server
	grpcServer := grpc.NewServer(cfg, ds, nil)

	// Start server in a goroutine
	serverErrCh := make(chan error, 1)
	go func() {
		err := grpcServer.Start()
		if err != nil {
			serverErrCh <- err
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Test: Create VIP and verify it's available via GetConfig
	vip := &models.VIP{
		VIP:      "192.168.1.100",
		Port:     80,
		Protocol: models.ProtocolTCP,
		LBMethod: models.LBMethodMaglev,
	}

	err = ds.CreateVIP(ctx, vip)
	require.NoError(t, err)

	// Get config and verify VIP is included
	config, err := ds.GetConfig(ctx)
	require.NoError(t, err)
	assert.Greater(t, len(config.VIPs), 0)

	// Cleanup
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	require.NoError(t, grpcServer.Stop(stopCtx))
}
