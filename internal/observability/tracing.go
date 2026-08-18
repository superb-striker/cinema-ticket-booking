package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type TracingConfig struct {
	Enabled     bool
	ServiceName string
	Environment string
	SampleRatio float64
}

func ConfigureTracing(ctx context.Context, config TracingConfig) (func(context.Context) error, error) {
	if !config.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", config.ServiceName),
		attribute.String("deployment.environment.name", config.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("create tracing resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { slog.Error("OpenTelemetry error", "error", err) }))
	return provider.Shutdown, nil
}

func TraceHTTP(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http.request",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " HTTP" }),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" && r.URL.Path != "/metrics"
		}),
	)
}
