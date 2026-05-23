package grpc

import (
	"context"
	"crypto/x509"

	controllerauth "github.com/akam1o/arca-lb/internal/controller/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	authorizationMetadataKey = "authorization"
	apiKeyMetadataKey        = "x-api-key"
)

func apiKeyUnaryServerInterceptor(expectedKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := authenticateIncomingContext(ctx, expectedKey); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func apiKeyStreamServerInterceptor(expectedKey string) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authenticateIncomingContext(stream.Context(), expectedKey); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

func authenticateIncomingContext(ctx context.Context, expectedKey string) error {
	if expectedKey == "" {
		return nil
	}
	if !controllerauth.APIKeyMatches(extractIncomingAPIKey(ctx), expectedKey) {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	return nil
}

func extractIncomingAPIKey(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	return controllerauth.ExtractAPIKey(
		md.Get(authorizationMetadataKey),
		md.Get(apiKeyMetadataKey),
	)
}

func authorizeAgentIDWithClientCert(ctx context.Context, agentID string) error {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "client certificate is required")
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return status.Error(codes.Unauthenticated, "client certificate is required")
	}

	for _, cert := range tlsInfo.State.PeerCertificates {
		if certificateMatchesAgentID(cert, agentID) {
			return nil
		}
	}

	return status.Error(codes.PermissionDenied, "agent_id does not match client certificate identity")
}

func certificateMatchesAgentID(cert *x509.Certificate, agentID string) bool {
	if cert == nil {
		return false
	}
	if cert.Subject.CommonName == agentID {
		return true
	}
	for _, name := range cert.DNSNames {
		if name == agentID {
			return true
		}
	}
	for _, ip := range cert.IPAddresses {
		if ip.String() == agentID {
			return true
		}
	}
	for _, uri := range cert.URIs {
		if uri.String() == agentID {
			return true
		}
	}
	return false
}
