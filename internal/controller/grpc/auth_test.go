package grpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthenticateIncomingContextRejectsMalformedAuthorizationBeforeXAPIKey(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		authorizationMetadataKey, "Basic controller-grpc-secret",
		apiKeyMetadataKey, "controller-grpc-secret",
	))

	err := authenticateIncomingContext(ctx, "controller-grpc-secret")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("authenticateIncomingContext error = %v, want unauthenticated", err)
	}
}

func TestAuthenticateIncomingContextAcceptsXAPIKeyWithoutAuthorization(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		apiKeyMetadataKey, "controller-grpc-secret",
	))

	if err := authenticateIncomingContext(ctx, "controller-grpc-secret"); err != nil {
		t.Fatalf("authenticateIncomingContext with x-api-key: %v", err)
	}
}
