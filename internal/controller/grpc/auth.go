package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
	if !apiKeyMatches(extractIncomingAPIKey(ctx), expectedKey) {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	return nil
}

func extractIncomingAPIKey(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, value := range md.Get(authorizationMetadataKey) {
		fields := strings.Fields(value)
		if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
			return fields[1]
		}
	}
	for _, value := range md.Get(apiKeyMetadataKey) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func apiKeyMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}
