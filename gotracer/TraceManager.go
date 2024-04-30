package gotracer

import (
	"context"
	"errors"

	"github.com/carwale/golibraries/gologger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
)

type CustomTracer struct {
	serviceName    string
	isInKubernetes bool
	collectorHost  string
	traceContext   context.Context
	traceProvider  *trace.TracerProvider
	logger         *gologger.CustomLogger
	sampler        trace.Sampler
	propagator     propagation.TextMapPropagator
	exporter       *otlptrace.Exporter
}

type Option func(t *CustomTracer)

func SetLogger(logger *gologger.CustomLogger) Option {
	return func(t *CustomTracer) { t.logger = logger }
}

func SetServiceName(serviceName string) Option {
	return func(t *CustomTracer) {
		if serviceName == "" {
			t.logger.LogError("service name cannot be empty for tracing", errors.New("InvalidArgument: service name cannot be empty"))
		}
		t.serviceName = serviceName
	}
}

func SetIsInKubernetes(isInKubernetes bool) Option {
	return func(t *CustomTracer) { t.isInKubernetes = isInKubernetes }
}

func SetCollectorHost(collectorHost string) Option {
	return func(t *CustomTracer) {
		if collectorHost == "" {
			t.logger.LogError("collectorHost cannot be empty for setting collector endpoint", errors.New("InvalidArgument: collectorHost cannot be empty"))
		}
		t.collectorHost = collectorHost
	}
}

func SetTracingContext(ctx context.Context) Option {
	return func(t *CustomTracer) { t.traceContext = ctx }
}

func SetSampler(sampler trace.Sampler) Option {
	return func(t *CustomTracer) { t.sampler = sampler }
}

func SetPropagator(propagator propagation.TextMapPropagator) Option {
	return func(t *CustomTracer) { t.propagator = propagator }
}

func SetOtelExporter(exporter *otlptrace.Exporter) Option {
	return func(t *CustomTracer) { t.exporter = exporter }
}

func NewCustomTracer(traceOptions ...Option) *CustomTracer {
	customTracer := &CustomTracer{
		sampler:      trace.AlwaysSample(),
		propagator:   propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
		traceContext: context.Background(),
	}
	for _, option := range traceOptions {
		option(customTracer)
	}
	if !customTracer.isInKubernetes {
		customTracer.logger.LogError("cannot enable tracing, as service is not inside kubernetes", errors.New("cannot enable tracing service not inside kubernetes"))
		return nil
	}

	res, err := resource.New(customTracer.traceContext, resource.WithAttributes(semconv.ServiceName(customTracer.serviceName)))
	if err != nil {
		customTracer.logger.LogError("could not set service name for tracing", err)
		return nil
	}

	exporter, err := otlptracegrpc.New(customTracer.traceContext, otlptracegrpc.WithEndpointURL("http://"+customTracer.collectorHost+":4317"), otlptracegrpc.WithInsecure())
	if err != nil {
		customTracer.logger.LogError("could not initialize otel exporter for tracing", err)
		return nil
	}

	traceProvider := trace.NewTracerProvider(trace.WithResource(res), trace.WithBatcher(exporter), trace.WithSampler(customTracer.sampler))
	otel.SetTracerProvider(traceProvider)
	otel.SetTextMapPropagator(customTracer.propagator)
	customTracer.traceProvider = traceProvider
	customTracer.exporter = exporter
	return customTracer
}

func (t *CustomTracer) Shutdown() {
	t.traceProvider.Shutdown(t.traceContext)
	t.exporter.Shutdown(t.traceContext)
}
