// Package otelsetup provides a unified OpenTelemetry initialization for arca-lb components.
package otelsetup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config configures OpenTelemetry.
type Config struct {
	// ServiceName is the service.name resource attribute.
	ServiceName string

	// ServiceVersion is the service.version attribute.
	ServiceVersion string

	// OTLPEndpoint is the gRPC endpoint for OTLP trace export.
	// If empty, tracing is disabled.
	OTLPEndpoint string

	// OTLPInsecure disables TLS for OTLP trace export.
	OTLPInsecure bool

	// MetricsEnabled enables Prometheus metrics export.
	MetricsEnabled bool
}

// Shutdown aggregates cleanup functions.
type Shutdown struct {
	fns []func(context.Context) error
}

// Shutdown calls all cleanup functions.
func (s *Shutdown) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, fn := range s.fns {
		if err := fn(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Setup initialises OpenTelemetry tracers and meters globally.
func Setup(ctx context.Context, cfg Config) (*Shutdown, error) {
	shutdown := &Shutdown{}
	logger := slog.Default().With("component", "otel")

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	// --- Traces ---
	if cfg.OTLPEndpoint != "" {
		traceOptions := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		}
		if cfg.OTLPInsecure {
			traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		}

		exporter, err := otlptracegrpc.New(ctx, traceOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter,
				sdktrace.WithBatchTimeout(5*time.Second),
			),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
		)
		otel.SetTracerProvider(tp)
		shutdown.fns = append(shutdown.fns, tp.Shutdown)
		logger.Info("OTLP trace exporter configured", "endpoint", cfg.OTLPEndpoint, "insecure", cfg.OTLPInsecure)
	} else {
		logger.Info("tracing disabled (no OTLP endpoint)")
	}

	// --- Metrics ---
	if cfg.MetricsEnabled {
		promExporter, err := prometheus.New()
		if err != nil {
			return nil, fmt.Errorf("failed to create Prometheus exporter: %w", err)
		}

		mp := otelmetric.NewMeterProvider(
			otelmetric.WithReader(promExporter),
			otelmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
		shutdown.fns = append(shutdown.fns, mp.Shutdown)
		logger.Info("Prometheus metrics exporter configured")
	}

	return shutdown, nil
}
