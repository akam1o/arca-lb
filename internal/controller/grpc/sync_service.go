package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ConfigSyncService implements the ConfigSync gRPC service
type ConfigSyncService struct {
	pb.UnimplementedConfigSyncServer
	datastore                      datastore.DataStore
	logger                         *logrus.Logger
	authorizeAgentIDWithClientCert bool
}

// ConfigSyncServiceOption configures ConfigSyncService behavior.
type ConfigSyncServiceOption func(*ConfigSyncService)

// WithAgentIDClientCertAuthorization requires agent_id to match the client certificate identity.
func WithAgentIDClientCertAuthorization(enabled bool) ConfigSyncServiceOption {
	return func(s *ConfigSyncService) {
		s.authorizeAgentIDWithClientCert = enabled
	}
}

// NewConfigSyncService creates a new ConfigSyncService
func NewConfigSyncService(ds datastore.DataStore, logger *logrus.Logger, opts ...ConfigSyncServiceOption) *ConfigSyncService {
	service := &ConfigSyncService{
		datastore: ds,
		logger:    logger,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// GetConfig returns the current configuration snapshot
func (s *ConfigSyncService) GetConfig(ctx context.Context, req *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := validateAgentID(req.AgentId); err != nil {
		return nil, err
	}
	if s.authorizeAgentIDWithClientCert {
		if err := s.authorizeAgentID(ctx, req.AgentId); err != nil {
			return nil, err
		}
	}

	s.logger.WithFields(logrus.Fields{
		"agent_id":         req.AgentId,
		"current_revision": req.CurrentRevision,
	}).Info("GetConfig called")

	revision, err := s.datastore.GetRevision(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get revision")
		return nil, status.Errorf(codes.Internal, "failed to get revision: %v", err)
	}

	if req.CurrentRevision > 0 && req.CurrentRevision == revision {
		return &pb.GetConfigResponse{
			Unchanged: true,
		}, nil
	}

	// Get configuration from datastore
	config, err := s.datastore.GetConfig(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get config")
		return nil, status.Errorf(codes.Internal, "failed to get config: %v", err)
	}

	if req.CurrentRevision > 0 && req.CurrentRevision == config.Revision {
		return &pb.GetConfigResponse{
			Unchanged: true,
		}, nil
	}

	// Convert to protobuf
	pbConfig, err := s.convertConfigToProto(config)
	if err != nil {
		s.logger.WithError(err).Error("Failed to convert config to proto")
		return nil, status.Errorf(codes.Internal, "failed to convert config: %v", err)
	}

	return &pb.GetConfigResponse{
		Config: pbConfig,
	}, nil
}

// WatchConfig streams configuration changes to the client
func (s *ConfigSyncService) WatchConfig(req *pb.WatchConfigRequest, stream pb.ConfigSync_WatchConfigServer) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	if err := validateAgentID(req.AgentId); err != nil {
		return err
	}
	if err := s.authorizeAgentID(stream.Context(), req.AgentId); err != nil {
		return err
	}

	s.logger.WithFields(logrus.Fields{
		"agent_id":         req.AgentId,
		"current_revision": req.CurrentRevision,
	}).Info("WatchConfig called")

	// Start watching before reading the initial config so updates that arrive
	// during the initial snapshot are still delivered.
	events, err := s.datastore.Watch(stream.Context())
	if err != nil {
		s.logger.WithError(err).Error("Failed to start watch")
		return status.Errorf(codes.Internal, "failed to start watch: %v", err)
	}

	// Get current config first
	config, err := s.datastore.GetConfig(stream.Context())
	if err != nil {
		s.logger.WithError(err).Error("Failed to get initial config")
		return status.Errorf(codes.Internal, "failed to get config: %v", err)
	}

	// Send current config if revision is different
	if config.Revision != req.CurrentRevision {
		pbConfig, err := s.convertConfigToProto(config)
		if err != nil {
			s.logger.WithError(err).Error("Failed to convert config to proto")
			return status.Errorf(codes.Internal, "failed to convert config: %v", err)
		}

		if err := stream.Send(&pb.WatchConfigResponse{
			Config: pbConfig,
		}); err != nil {
			s.logger.WithError(err).Error("Failed to send initial config")
			return err
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			s.logger.Info("WatchConfig stream closed by client")
			return nil
		case event, ok := <-events:
			if !ok {
				s.logger.Info("WatchConfig event channel closed")
				return nil
			}

			s.logger.WithField("event_type", event.Type).Debug("Received watch event")
			if event.Type == datastore.EventTypeError {
				if event.Error == nil {
					s.logger.Error("Datastore watch failed")
					return status.Error(codes.Internal, "datastore watch error")
				}
				s.logger.WithError(event.Error).Error("Datastore watch failed")
				return status.Errorf(codes.Internal, "datastore watch error: %v", event.Error)
			}

			// Get updated config
			config, err := s.datastore.GetConfig(stream.Context())
			if err != nil {
				s.logger.WithError(err).Error("Failed to get updated config")
				continue
			}

			// Convert and send
			pbConfig, err := s.convertConfigToProto(config)
			if err != nil {
				s.logger.WithError(err).Error("Failed to convert updated config to proto")
				continue
			}

			if err := stream.Send(&pb.WatchConfigResponse{
				Config: pbConfig,
			}); err != nil {
				s.logger.WithError(err).Error("Failed to send config update")
				return err
			}
		}
	}
}

// RegisterAgent registers an agent with the controller
func (s *ConfigSyncService) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := validateAgentID(req.AgentId); err != nil {
		return nil, err
	}
	if err := s.authorizeAgentID(ctx, req.AgentId); err != nil {
		return nil, err
	}

	s.logger.WithFields(logrus.Fields{
		"agent_id": req.AgentId,
		"version":  req.Version,
		"metadata": req.Metadata,
	}).Info("RegisterAgent called")

	// Get current config
	config, err := s.datastore.GetConfig(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get config for registration")
		return &pb.RegisterAgentResponse{
			Success: false,
			Message: "Failed to get configuration",
		}, nil
	}

	// Convert to protobuf
	pbConfig, err := s.convertConfigToProto(config)
	if err != nil {
		s.logger.WithError(err).Error("Failed to convert config to proto")
		return &pb.RegisterAgentResponse{
			Success: false,
			Message: "Failed to convert configuration",
		}, nil
	}

	return &pb.RegisterAgentResponse{
		Success: true,
		Message: "Agent registered successfully",
		Config:  pbConfig,
	}, nil
}

// Heartbeat handles agent heartbeat requests
func (s *ConfigSyncService) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := validateAgentID(req.AgentId); err != nil {
		return nil, err
	}
	if err := s.authorizeAgentID(ctx, req.AgentId); err != nil {
		return nil, err
	}

	s.logger.WithFields(logrus.Fields{
		"agent_id":         req.AgentId,
		"current_revision": req.CurrentRevision,
	}).Debug("Heartbeat received")

	// Get current revision
	revision, err := s.datastore.GetRevision(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get revision")
		return nil, status.Errorf(codes.Internal, "failed to get revision: %v", err)
	}

	// Check if resync is needed
	resyncRequired := req.CurrentRevision != revision

	return &pb.HeartbeatResponse{
		Success:        true,
		NewRevision:    revision,
		ResyncRequired: resyncRequired,
	}, nil
}

func validateAgentID(agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return status.Error(codes.InvalidArgument, "agent_id is required")
	}
	return nil
}

func (s *ConfigSyncService) authorizeAgentID(ctx context.Context, agentID string) error {
	if !s.authorizeAgentIDWithClientCert {
		return nil
	}
	return authorizeAgentIDWithClientCert(ctx, agentID)
}

// convertConfigToProto converts internal config model to protobuf
func (s *ConfigSyncService) convertConfigToProto(config *models.Config) (*pb.ConfigSnapshot, error) {
	if err := validateConfigIdentity(config); err != nil {
		return nil, err
	}

	pbVips := make([]*pb.VIPConfig, 0, len(config.VIPs))

	for vipIndex, vipConfig := range config.VIPs {
		var pbDscp *wrapperspb.UInt32Value
		if vipConfig.VIP.DSCP != nil {
			pbDscp = wrapperspb.UInt32(uint32(*vipConfig.VIP.DSCP))
		}

		pbVip := &pb.VIP{
			Id:        vipConfig.VIP.ID,
			Vip:       vipConfig.VIP.VIP,
			Port:      int32(vipConfig.VIP.Port),
			Protocol:  s.convertProtocol(vipConfig.VIP.Protocol),
			LbMethod:  s.convertLBMethod(vipConfig.VIP.LBMethod),
			EncapType: s.convertEncapType(vipConfig.VIP.EncapType),
			Dscp:      pbDscp,
			CreatedAt: timestamppb.New(vipConfig.VIP.CreatedAt),
			UpdatedAt: timestamppb.New(vipConfig.VIP.UpdatedAt),
		}

		// Convert health check if present
		var pbHealthCheck *pb.HealthCheck
		if vipConfig.HealthCheck != nil {
			intervalSec, err := healthCheckInt32("interval_sec", vipConfig.HealthCheck.IntervalSec, models.MaxHealthCheckSeconds)
			if err != nil {
				return nil, err
			}
			timeoutSec, err := healthCheckInt32("timeout_sec", vipConfig.HealthCheck.TimeoutSec, models.MaxHealthCheckSeconds)
			if err != nil {
				return nil, err
			}
			riseCount, err := healthCheckInt32("rise_count", vipConfig.HealthCheck.RiseCount, models.MaxHealthCheckCount)
			if err != nil {
				return nil, err
			}
			fallCount, err := healthCheckInt32("fall_count", vipConfig.HealthCheck.FallCount, models.MaxHealthCheckCount)
			if err != nil {
				return nil, err
			}

			// Convert HCConfig map to JSON string
			configJSON := ""
			if vipConfig.HealthCheck.Config != nil {
				jsonBytes, err := json.Marshal(vipConfig.HealthCheck.Config)
				if err != nil {
					return nil, fmt.Errorf("health check config at vip index %d is invalid: %w", vipIndex, err)
				}
				configJSON = string(jsonBytes)
			}

			pbHealthCheck = &pb.HealthCheck{
				Id:          vipConfig.HealthCheck.ID,
				VipId:       vipConfig.HealthCheck.VIPID,
				Type:        s.convertHCType(vipConfig.HealthCheck.Type),
				IntervalSec: intervalSec,
				TimeoutSec:  timeoutSec,
				RiseCount:   riseCount,
				FallCount:   fallCount,
				Config:      configJSON,
				CreatedAt:   timestamppb.New(vipConfig.HealthCheck.CreatedAt),
				UpdatedAt:   timestamppb.New(vipConfig.HealthCheck.UpdatedAt),
			}
		}

		// Convert backends
		pbBackends := make([]*pb.Backend, 0, len(vipConfig.Backends))
		for _, backend := range vipConfig.Backends {
			pbBackends = append(pbBackends, &pb.Backend{
				Id:        backend.ID,
				VipId:     backend.VIPID,
				Ip:        backend.IP,
				Weight:    int32(backend.Weight),
				CreatedAt: timestamppb.New(backend.CreatedAt),
				UpdatedAt: timestamppb.New(backend.UpdatedAt),
			})
		}

		pbVips = append(pbVips, &pb.VIPConfig{
			Vip:         pbVip,
			HealthCheck: pbHealthCheck,
			Backends:    pbBackends,
		})
	}

	return &pb.ConfigSnapshot{
		Revision: config.Revision,
		Vips:     pbVips,
	}, nil
}

func healthCheckInt32(field string, value, max int) (int32, error) {
	if value < 1 || value > max {
		return 0, fmt.Errorf("health check %s must be between 1 and %d, got %d", field, max, value)
	}
	return int32(value), nil
}

type backendConfigLocation struct {
	vipIndex     int
	backendIndex int
}

func validateConfigIdentity(config *models.Config) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}

	seenVIPIDs := make(map[string]int, len(config.VIPs))
	seenVIPTuples := make(map[string]int, len(config.VIPs))
	seenBackendIDs := make(map[string]backendConfigLocation)
	for vipIndex := range config.VIPs {
		vipConfig := &config.VIPs[vipIndex]
		if vipConfig.VIP.ID == "" {
			return fmt.Errorf("vip config at index %d id is required", vipIndex)
		}
		if firstIndex, ok := seenVIPIDs[vipConfig.VIP.ID]; ok {
			return fmt.Errorf("vip config at index %d duplicates vip id %q first seen at index %d", vipIndex, vipConfig.VIP.ID, firstIndex)
		}
		seenVIPIDs[vipConfig.VIP.ID] = vipIndex

		tupleKey := vipTupleKey(vipConfig.VIP)
		if firstIndex, ok := seenVIPTuples[tupleKey]; ok {
			return fmt.Errorf("vip config at index %d duplicates vip tuple %s first seen at index %d", vipIndex, tupleKey, firstIndex)
		}
		seenVIPTuples[tupleKey] = vipIndex

		if vipConfig.HealthCheck != nil {
			if vipConfig.HealthCheck.VIPID == "" {
				return fmt.Errorf("health check at vip index %d vip_id is required", vipIndex)
			}
			if vipConfig.HealthCheck.VIPID != vipConfig.VIP.ID {
				return fmt.Errorf("health check at vip index %d vip_id %q does not match vip id %q", vipIndex, vipConfig.HealthCheck.VIPID, vipConfig.VIP.ID)
			}
		}

		seenBackendIPs := make(map[string]int, len(vipConfig.Backends))
		for backendIndex, backend := range vipConfig.Backends {
			if backend.ID == "" {
				return fmt.Errorf("backend at vip index %d backend index %d id is required", vipIndex, backendIndex)
			}
			if backend.VIPID == "" {
				return fmt.Errorf("backend at vip index %d backend index %d vip_id is required", vipIndex, backendIndex)
			}
			if backend.VIPID != vipConfig.VIP.ID {
				return fmt.Errorf("backend at vip index %d backend index %d vip_id %q does not match vip id %q", vipIndex, backendIndex, backend.VIPID, vipConfig.VIP.ID)
			}
			if firstLocation, ok := seenBackendIDs[backend.ID]; ok {
				return fmt.Errorf("backend at vip index %d backend index %d duplicates backend id %q first seen at vip index %d backend index %d", vipIndex, backendIndex, backend.ID, firstLocation.vipIndex, firstLocation.backendIndex)
			}
			seenBackendIDs[backend.ID] = backendConfigLocation{
				vipIndex:     vipIndex,
				backendIndex: backendIndex,
			}

			backendIP := net.ParseIP(backend.IP)
			if backendIP == nil {
				continue
			}
			backendIPKey := backendIP.String()
			if firstIndex, ok := seenBackendIPs[backendIPKey]; ok {
				return fmt.Errorf("backend at vip index %d backend index %d duplicates backend ip %q first seen at backend index %d", vipIndex, backendIndex, backendIPKey, firstIndex)
			}
			seenBackendIPs[backendIPKey] = backendIndex
		}
	}

	return nil
}

func vipTupleKey(vip models.VIP) string {
	vipIP := net.ParseIP(vip.VIP)
	if vipIP != nil {
		return fmt.Sprintf("%s/%d/%s", vipIP.String(), vip.Port, vip.Protocol)
	}
	return fmt.Sprintf("%s/%d/%s", vip.VIP, vip.Port, vip.Protocol)
}

// convertProtocol converts internal protocol to protobuf
func (s *ConfigSyncService) convertProtocol(protocol models.Protocol) pb.Protocol {
	switch protocol {
	case models.ProtocolTCP:
		return pb.Protocol_PROTOCOL_TCP
	case models.ProtocolUDP:
		return pb.Protocol_PROTOCOL_UDP
	default:
		return pb.Protocol_PROTOCOL_UNSPECIFIED
	}
}

// convertLBMethod converts internal LB method to protobuf
func (s *ConfigSyncService) convertLBMethod(method models.LBMethod) pb.LBMethod {
	switch method {
	case models.LBMethodMaglev:
		return pb.LBMethod_LB_METHOD_MAGLEV
	default:
		return pb.LBMethod_LB_METHOD_UNSPECIFIED
	}
}

// convertEncapType converts internal encap type to protobuf
func (s *ConfigSyncService) convertEncapType(encapType models.EncapType) pb.EncapType {
	switch encapType {
	case models.EncapTypeGRE4:
		return pb.EncapType_ENCAP_TYPE_GRE4
	case models.EncapTypeGRE6:
		return pb.EncapType_ENCAP_TYPE_GRE6
	case models.EncapTypeL3DSR:
		return pb.EncapType_ENCAP_TYPE_L3DSR
	case models.EncapTypeNAT4:
		return pb.EncapType_ENCAP_TYPE_NAT4
	case models.EncapTypeNAT6:
		return pb.EncapType_ENCAP_TYPE_NAT6
	default:
		return pb.EncapType_ENCAP_TYPE_UNSPECIFIED
	}
}

// convertHCType converts internal health check type to protobuf
func (s *ConfigSyncService) convertHCType(hcType models.HCType) pb.HCType {
	switch hcType {
	case models.HCTypeHTTP:
		return pb.HCType_HC_TYPE_HTTP
	case models.HCTypeHTTPS:
		return pb.HCType_HC_TYPE_HTTPS
	case models.HCTypeTCP:
		return pb.HCType_HC_TYPE_TCP
	case models.HCTypePing:
		return pb.HCType_HC_TYPE_PING
	case models.HCTypeTLSHello:
		return pb.HCType_HC_TYPE_TLS_HELLO
	default:
		return pb.HCType_HC_TYPE_UNSPECIFIED
	}
}
