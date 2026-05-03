package otelsetup

import (
	"context"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type recordingTraceService struct {
	tracepb.UnimplementedTraceServiceServer
	exported chan struct{}
}

func (s *recordingTraceService) Export(context.Context, *tracepb.ExportTraceServiceRequest) (*tracepb.ExportTraceServiceResponse, error) {
	select {
	case s.exported <- struct{}{}:
	default:
	}
	return &tracepb.ExportTraceServiceResponse{}, nil
}

func TestSetupExportsToPlaintextOTLPWhenInsecureEnabled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	server := grpc.NewServer()
	traceService := &recordingTraceService{exported: make(chan struct{}, 1)}
	tracepb.RegisterTraceServiceServer(server, traceService)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	ctx := context.Background()
	shutdown, err := Setup(ctx, Config{
		ServiceName:    "arca-lb-test",
		ServiceVersion: "test",
		OTLPEndpoint:   listener.Addr().String(),
		OTLPInsecure:   true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_, span := otel.Tracer("arca-lb-test").Start(ctx, "test-span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := shutdown.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case <-traceService.exported:
	default:
		t.Fatal("plaintext OTLP collector did not receive exported trace")
	}
}
