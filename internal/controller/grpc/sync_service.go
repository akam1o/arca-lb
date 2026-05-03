package grpc

import (
	"context"
	"encoding/json"

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
	datastore datastore.DataStore
	logger    *logrus.Logger
}

// NewConfigSyncService creates a new ConfigSyncService
func NewConfigSyncService(ds datastore.DataStore, logger *logrus.Logger) *ConfigSyncService {
	return &ConfigSyncService{
		datastore: ds,
		logger:    logger,
	}
}

// GetConfig returns the current configuration snapshot
func (s *ConfigSyncService) GetConfig(ctx context.Context, req *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	s.logger.Info("GetConfig called")

	// Get configuration from datastore
	config, err := s.datastore.GetConfig(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get config")
		return nil, status.Errorf(codes.Internal, "failed to get config: %v", err)
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

// convertConfigToProto converts internal config model to protobuf
func (s *ConfigSyncService) convertConfigToProto(config *models.Config) (*pb.ConfigSnapshot, error) {
	pbVips := make([]*pb.VIPConfig, 0, len(config.VIPs))

	for _, vipConfig := range config.VIPs {
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
			// Convert HCConfig map to JSON string
			configJSON := ""
			if vipConfig.HealthCheck.Config != nil {
				if jsonBytes, err := json.Marshal(vipConfig.HealthCheck.Config); err == nil {
					configJSON = string(jsonBytes)
				}
			}

			pbHealthCheck = &pb.HealthCheck{
				Id:          vipConfig.HealthCheck.ID,
				VipId:       vipConfig.HealthCheck.VIPID,
				Type:        s.convertHCType(vipConfig.HealthCheck.Type),
				IntervalSec: int32(vipConfig.HealthCheck.IntervalSec),
				TimeoutSec:  int32(vipConfig.HealthCheck.TimeoutSec),
				RiseCount:   int32(vipConfig.HealthCheck.RiseCount),
				FallCount:   int32(vipConfig.HealthCheck.FallCount),
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
	default:
		return pb.HCType_HC_TYPE_UNSPECIFIED
	}
}
