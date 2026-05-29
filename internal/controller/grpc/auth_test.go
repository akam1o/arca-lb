package grpc

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"net/url"
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

func TestAuthenticateIncomingContextRejectsMultipleAuthorizationValues(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		authorizationMetadataKey, "Bearer controller-grpc-secret",
		authorizationMetadataKey, "Bearer wrong-controller-grpc-secret",
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

func TestAuthenticateIncomingContextRejectsMultipleXAPIKeyValues(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		apiKeyMetadataKey, "controller-grpc-secret",
		apiKeyMetadataKey, "wrong-controller-grpc-secret",
	))

	err := authenticateIncomingContext(ctx, "controller-grpc-secret")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("authenticateIncomingContext error = %v, want unauthenticated", err)
	}
}

func TestCertificateMatchesAgentIDRequiresSANIdentity(t *testing.T) {
	uri, err := url.Parse("spiffe://arca-lb/agent-1")
	if err != nil {
		t.Fatalf("Parse URI: %v", err)
	}

	tests := []struct {
		name    string
		cert    *x509.Certificate
		agentID string
		want    bool
	}{
		{
			name: "common name only is rejected",
			cert: &x509.Certificate{
				Subject: pkix.Name{CommonName: "agent-1"},
			},
			agentID: "agent-1",
		},
		{
			name: "dns san",
			cert: &x509.Certificate{
				DNSNames: []string{"agent-1"},
			},
			agentID: "agent-1",
			want:    true,
		},
		{
			name: "ip san",
			cert: &x509.Certificate{
				IPAddresses: []net.IP{net.ParseIP("192.0.2.10")},
			},
			agentID: "192.0.2.10",
			want:    true,
		},
		{
			name: "uri san",
			cert: &x509.Certificate{
				URIs: []*url.URL{uri},
			},
			agentID: "spiffe://arca-lb/agent-1",
			want:    true,
		},
		{
			name:    "nil certificate",
			agentID: "agent-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := certificateMatchesAgentID(tt.cert, tt.agentID)
			if got != tt.want {
				t.Fatalf("certificateMatchesAgentID() = %v, want %v", got, tt.want)
			}
		})
	}
}
