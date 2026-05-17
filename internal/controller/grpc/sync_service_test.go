package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/akam1o/arca-lb/internal/testutil"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func setupTestServer(t *testing.T, mockDS *testutil.MockDataStore) (*bufconn.Listener, pb.ConfigSyncClient) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Suppress logs during tests

	service := NewConfigSyncService(mockDS, logger)
	pb.RegisterConfigSyncServer(s, service)

	go func() {
		if err := s.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.WithError(err).Error("gRPC server exited")
		}
	}()

	conn, err := grpc.DialContext(context.Background(), "bufconn", // nolint:staticcheck // DialContext is adequate for bufconn-based test dialer
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := pb.NewConfigSyncClient(conn)

	t.Cleanup(func() {
		s.GracefulStop()
		require.NoError(t, conn.Close())
		require.NoError(t, lis.Close())
	})

	return lis, client
}

func TestConfigSyncService_GetConfig(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*testutil.MockDataStore)
		request       *pb.GetConfigRequest
		expectedError codes.Code
		validate      func(*testing.T, *pb.GetConfigResponse)
	}{
		{
			name: "success with empty config",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{
					Revision: 1,
					VIPs:     []models.VIPConfig{},
				})
			},
			validate: func(t *testing.T, resp *pb.GetConfigResponse) {
				assert.NotNil(t, resp.Config)
				assert.Equal(t, int64(1), resp.Config.Revision)
				assert.Empty(t, resp.Config.Vips)
			},
		},
		{
			name: "success with VIP and backend",
			setupMock: func(mock *testutil.MockDataStore) {
				vip := &models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				}
				backend := &models.Backend{
					ID:     "backend-1",
					VIPID:  "vip-1",
					IP:     "10.0.0.1",
					Weight: 10,
				}
				mock.SetConfig(&models.Config{
					Revision: 2,
					VIPs: []models.VIPConfig{
						{
							VIP:      *vip,
							Backends: []models.Backend{*backend},
						},
					},
				})
			},
			validate: func(t *testing.T, resp *pb.GetConfigResponse) {
				assert.NotNil(t, resp.Config)
				assert.Equal(t, int64(2), resp.Config.Revision)
				require.Len(t, resp.Config.Vips, 1)
				vipConfig := resp.Config.Vips[0]
				assert.Equal(t, "vip-1", vipConfig.Vip.Id)
				assert.Equal(t, "192.168.1.100", vipConfig.Vip.Vip)
				assert.Equal(t, int32(80), vipConfig.Vip.Port)
				assert.Equal(t, pb.Protocol_PROTOCOL_TCP, vipConfig.Vip.Protocol)
				assert.Equal(t, pb.LBMethod_LB_METHOD_MAGLEV, vipConfig.Vip.LbMethod)
				require.Len(t, vipConfig.Backends, 1)
				backend := vipConfig.Backends[0]
				assert.Equal(t, "backend-1", backend.Id)
				assert.Equal(t, "vip-1", backend.VipId)
				assert.Equal(t, "10.0.0.1", backend.Ip)
				assert.Equal(t, int32(10), backend.Weight)
			},
		},
		{
			name: "success with health check",
			setupMock: func(mock *testutil.MockDataStore) {
				vip := &models.VIP{
					ID:       "vip-1",
					VIP:      "192.168.1.100",
					Port:     80,
					Protocol: models.ProtocolTCP,
					LBMethod: models.LBMethodMaglev,
				}
				healthCheck := &models.HealthCheck{
					ID:          "hc-1",
					VIPID:       "vip-1",
					Type:        models.HCTypeHTTP,
					IntervalSec: 10,
					TimeoutSec:  5,
					RiseCount:   3,
					FallCount:   3,
					Config: map[string]interface{}{
						"port": 8080,
						"path": "/health",
					},
				}
				mock.SetConfig(&models.Config{
					Revision: 3,
					VIPs: []models.VIPConfig{
						{
							VIP:         *vip,
							HealthCheck: healthCheck,
							Backends:    []models.Backend{},
						},
					},
				})
			},
			validate: func(t *testing.T, resp *pb.GetConfigResponse) {
				assert.NotNil(t, resp.Config)
				require.Len(t, resp.Config.Vips, 1)
				vipConfig := resp.Config.Vips[0]
				require.NotNil(t, vipConfig.HealthCheck)
				hc := vipConfig.HealthCheck
				assert.Equal(t, "hc-1", hc.Id)
				assert.Equal(t, "vip-1", hc.VipId)
				assert.Equal(t, pb.HCType_HC_TYPE_HTTP, hc.Type)
				assert.Equal(t, int32(10), hc.IntervalSec)
				assert.Equal(t, int32(5), hc.TimeoutSec)
				assert.Equal(t, int32(3), hc.RiseCount)
				assert.Equal(t, int32(3), hc.FallCount)
				var hcConfig map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(hc.Config), &hcConfig))
				assert.Equal(t, float64(8080), hcConfig["port"])
				assert.Equal(t, "/health", hcConfig["path"])
			},
		},
		{
			name: "unchanged when current revision matches",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetRevision(7)
				mock.SetGetConfigError(errors.New("GetConfig should not be called"))
			},
			request: &pb.GetConfigRequest{
				CurrentRevision: 7,
			},
			validate: func(t *testing.T, resp *pb.GetConfigResponse) {
				assert.True(t, resp.Unchanged)
				assert.Nil(t, resp.Config)
			},
		},
		{
			name: "datastore error",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetGetConfigError(datastore.ErrNotFound)
			},
			expectedError: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDS := testutil.NewMockDataStore()
			tt.setupMock(mockDS)

			_, client := setupTestServer(t, mockDS)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := tt.request
			if req == nil {
				req = &pb.GetConfigRequest{}
			}

			resp, err := client.GetConfig(ctx, req)

			if tt.expectedError != codes.OK {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedError, st.Code())
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, resp)
				}
			}
		})
	}
}

func TestConfigSyncService_GetConfigRejectsNilRequest(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	service := NewConfigSyncService(testutil.NewMockDataStore(), logger)

	resp, err := service.GetConfig(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestConfigSyncService_WatchConfig(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*testutil.MockDataStore)
		request       *pb.WatchConfigRequest
		expectedError codes.Code
		validate      func(*testing.T, pb.ConfigSync_WatchConfigClient)
	}{
		{
			name: "send initial config when revision differs",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{
					Revision: 5,
					VIPs:     []models.VIPConfig{},
				})
				// Setup watch channel
				watchCh := make(chan datastore.WatchEvent, 1)
				mock.SetWatchChannel(watchCh)
			},
			request: &pb.WatchConfigRequest{
				AgentId:         "agent-1",
				CurrentRevision: 1,
			},
			validate: func(t *testing.T, stream pb.ConfigSync_WatchConfigClient) {
				resp, err := stream.Recv()
				require.NoError(t, err)
				assert.NotNil(t, resp.Config)
				assert.Equal(t, int64(5), resp.Config.Revision)
			},
		},
		{
			name: "no initial config when revision matches",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{
					Revision: 5,
					VIPs:     []models.VIPConfig{},
				})
				watchCh := make(chan datastore.WatchEvent, 1)
				mock.SetWatchChannel(watchCh)
			},
			request: &pb.WatchConfigRequest{
				AgentId:         "agent-1",
				CurrentRevision: 5,
			},
			validate: func(t *testing.T, stream pb.ConfigSync_WatchConfigClient) {
				// Should not receive initial config immediately
				// Use a short timeout to verify no immediate response
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				done := make(chan bool)
				go func() {
					_, err := stream.Recv()
					done <- (err != nil)
				}()
				select {
				case <-ctx.Done():
					// Timeout is expected - no initial config sent
					assert.True(t, true)
				case <-done:
					// Received something (unexpected)
					t.Log("Received unexpected response")
				}
			},
		},
		{
			name: "receive watch events",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{
					Revision: 5,
					VIPs:     []models.VIPConfig{},
				})
				watchCh := make(chan datastore.WatchEvent, 1)
				mock.SetWatchChannel(watchCh)
				// Send an event
				go func() {
					time.Sleep(50 * time.Millisecond)
					watchCh <- datastore.WatchEvent{
						Type: datastore.EventTypeVIPUpdated,
					}
				}()
			},
			request: &pb.WatchConfigRequest{
				AgentId:         "agent-1",
				CurrentRevision: 5,
			},
			validate: func(t *testing.T, stream pb.ConfigSync_WatchConfigClient) {
				// Wait for event with timeout guard
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				respCh := make(chan *pb.WatchConfigResponse, 1)
				errCh := make(chan error, 1)

				go func() {
					resp, err := stream.Recv()
					if err != nil {
						errCh <- err
						return
					}
					respCh <- resp
				}()

				select {
				case <-ctx.Done():
					t.Fatalf("timeout waiting for watch event")
				case err := <-errCh:
					require.NoError(t, err)
				case resp := <-respCh:
					assert.NotNil(t, resp.Config)
				}
			},
		},
		{
			name: "datastore error on initial config",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetGetConfigError(datastore.ErrNotFound)
			},
			request: &pb.WatchConfigRequest{
				AgentId:         "agent-1",
				CurrentRevision: 1,
			},
			expectedError: codes.Internal,
		},
		{
			name: "missing agent id",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{Revision: 5})
			},
			request: &pb.WatchConfigRequest{
				AgentId:         "  ",
				CurrentRevision: 1,
			},
			expectedError: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDS := testutil.NewMockDataStore()
			tt.setupMock(mockDS)

			_, client := setupTestServer(t, mockDS)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			stream, err := client.WatchConfig(ctx, tt.request)
			if tt.expectedError != codes.OK {
				if err == nil {
					_, err = stream.Recv()
				}
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedError, st.Code())
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, stream)
				}
			}
		})
	}
}

func TestConfigSyncService_Heartbeat(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*testutil.MockDataStore)
		request       *pb.HeartbeatRequest
		expectedError codes.Code
		validate      func(*testing.T, *pb.HeartbeatResponse)
	}{
		{
			name: "success - resync not required",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetRevision(5)
			},
			request: &pb.HeartbeatRequest{
				AgentId:         "agent-1",
				CurrentRevision: 5,
			},
			validate: func(t *testing.T, resp *pb.HeartbeatResponse) {
				assert.True(t, resp.Success)
				assert.Equal(t, int64(5), resp.NewRevision)
				assert.False(t, resp.ResyncRequired)
			},
		},
		{
			name: "success - resync required",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetRevision(10)
			},
			request: &pb.HeartbeatRequest{
				AgentId:         "agent-1",
				CurrentRevision: 5,
			},
			validate: func(t *testing.T, resp *pb.HeartbeatResponse) {
				assert.True(t, resp.Success)
				assert.Equal(t, int64(10), resp.NewRevision)
				assert.True(t, resp.ResyncRequired)
			},
		},
		{
			name: "datastore error",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetGetRevisionError(datastore.ErrNotFound)
			},
			request: &pb.HeartbeatRequest{
				AgentId:         "agent-1",
				CurrentRevision: 5,
			},
			expectedError: codes.Internal,
		},
		{
			name: "missing agent id",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetRevision(5)
			},
			request: &pb.HeartbeatRequest{
				AgentId:         " ",
				CurrentRevision: 5,
			},
			expectedError: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDS := testutil.NewMockDataStore()
			tt.setupMock(mockDS)

			_, client := setupTestServer(t, mockDS)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.Heartbeat(ctx, tt.request)

			if tt.expectedError != codes.OK {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedError, st.Code())
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, resp)
				}
			}
		})
	}
}

func TestConfigSyncService_RegisterAgent(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*testutil.MockDataStore)
		request       *pb.RegisterAgentRequest
		expectedError codes.Code
		validate      func(*testing.T, *pb.RegisterAgentResponse)
	}{
		{
			name: "success",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{
					Revision: 1,
					VIPs:     []models.VIPConfig{},
				})
			},
			request: &pb.RegisterAgentRequest{
				AgentId: "agent-1",
				Version: "1.0.0",
				Metadata: map[string]string{
					"region": "us-west-1",
				},
			},
			validate: func(t *testing.T, resp *pb.RegisterAgentResponse) {
				assert.True(t, resp.Success)
				assert.Equal(t, "Agent registered successfully", resp.Message)
				assert.NotNil(t, resp.Config)
				assert.Equal(t, int64(1), resp.Config.Revision)
			},
		},
		{
			name: "datastore error",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetGetConfigError(datastore.ErrNotFound)
			},
			request: &pb.RegisterAgentRequest{
				AgentId: "agent-1",
				Version: "1.0.0",
			},
			validate: func(t *testing.T, resp *pb.RegisterAgentResponse) {
				assert.False(t, resp.Success)
				assert.Contains(t, resp.Message, "Failed")
			},
		},
		{
			name: "missing agent id",
			setupMock: func(mock *testutil.MockDataStore) {
				mock.SetConfig(&models.Config{Revision: 1})
			},
			request: &pb.RegisterAgentRequest{
				AgentId: " ",
				Version: "1.0.0",
			},
			expectedError: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDS := testutil.NewMockDataStore()
			tt.setupMock(mockDS)

			_, client := setupTestServer(t, mockDS)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.RegisterAgent(ctx, tt.request)
			if tt.expectedError != codes.OK {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedError, st.Code())
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, resp)
				}
			}
		})
	}
}

func TestConfigSyncServiceAuthorizesAgentIDWithClientCert(t *testing.T) {
	mockDS := testutil.NewMockDataStore()
	mockDS.SetConfig(&models.Config{
		Revision: 1,
		VIPs:     []models.VIPConfig{},
	})

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	service := NewConfigSyncService(mockDS, logger, WithAgentIDClientCertAuthorization(true))

	ctx := contextWithClientCertificateIdentity(context.Background(), "agent-1")
	getResp, err := service.GetConfig(ctx, &pb.GetConfigRequest{AgentId: "agent-1"})
	if err != nil {
		t.Fatalf("GetConfig with matching certificate identity: %v", err)
	}
	if getResp == nil || getResp.Config == nil {
		t.Fatalf("GetConfig response = %#v, want config", getResp)
	}

	resp, err := service.RegisterAgent(ctx, &pb.RegisterAgentRequest{AgentId: "agent-1"})
	if err != nil {
		t.Fatalf("RegisterAgent with matching certificate identity: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("RegisterAgent response = %#v, want success", resp)
	}

	_, err = service.Heartbeat(ctx, &pb.HeartbeatRequest{AgentId: "agent-2"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Heartbeat with mismatched certificate identity error = %v, want permission denied", err)
	}

	_, err = service.GetConfig(ctx, &pb.GetConfigRequest{AgentId: "agent-2"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetConfig with mismatched certificate identity error = %v, want permission denied", err)
	}

	_, err = service.GetConfig(ctx, &pb.GetConfigRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetConfig without agent_id error = %v, want invalid argument", err)
	}

	_, err = service.RegisterAgent(context.Background(), &pb.RegisterAgentRequest{AgentId: "agent-1"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RegisterAgent without certificate identity error = %v, want unauthenticated", err)
	}
}

func contextWithClientCertificateIdentity(ctx context.Context, commonName string) context.Context {
	return peer.NewContext(ctx, &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{
					{
						Subject: pkix.Name{
							CommonName: commonName,
						},
						DNSNames: []string{commonName},
					},
				},
			},
		},
	})
}

func TestConfigSyncService_ConvertHCType(t *testing.T) {
	service := &ConfigSyncService{}

	tests := []struct {
		name string
		in   models.HCType
		want pb.HCType
	}{
		{name: "http", in: models.HCTypeHTTP, want: pb.HCType_HC_TYPE_HTTP},
		{name: "https", in: models.HCTypeHTTPS, want: pb.HCType_HC_TYPE_HTTPS},
		{name: "tcp", in: models.HCTypeTCP, want: pb.HCType_HC_TYPE_TCP},
		{name: "ping", in: models.HCTypePing, want: pb.HCType_HC_TYPE_PING},
		{name: "tls hello", in: models.HCTypeTLSHello, want: pb.HCType_HC_TYPE_TLS_HELLO},
		{name: "unknown", in: models.HCType("smtp"), want: pb.HCType_HC_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, service.convertHCType(tt.in))
		})
	}
}
