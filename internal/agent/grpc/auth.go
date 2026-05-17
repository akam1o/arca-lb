package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const authorizationMetadataKey = "authorization"

func apiKeyUnaryClientInterceptor(apiKey string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req interface{},
		reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return invoker(outgoingAPIKeyContext(ctx, apiKey), method, req, reply, cc, opts...)
	}
}

func apiKeyStreamClientInterceptor(apiKey string) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(outgoingAPIKeyContext(ctx, apiKey), desc, cc, method, opts...)
	}
}

func outgoingAPIKeyContext(ctx context.Context, apiKey string) context.Context {
	if apiKey == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+apiKey)
}
