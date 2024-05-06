package gotracer

import (
	"context"
	"errors"

	"github.com/carwale/golibraries/gologger"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
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
	resource       *resource.Resource
}

type Option func(t *CustomTracer)

func SetLogger(logger *gologger.CustomLogger) Option {
	return func(t *CustomTracer) { t.logger = logger }
}

func SetResource(resource *resource.Resource) Option {
	return func(t *CustomTracer) {
		if resource == nil {
			t.logger.LogError("resource cannot be nil", errors.New("InvalidArgument: resource cannot be nil"))
		} else {
			t.resource = resource
		}
	}
}

func SetServiceName(serviceName string) Option {
	return func(t *CustomTracer) {
		if serviceName == "" {
			t.logger.LogError("service name cannot be empty for tracing", errors.New("InvalidArgument: service name cannot be empty"))
		} else {
			t.serviceName = serviceName
		}
	}
}

func SetIsInKubernetes(isInKubernetes bool) Option {
	return func(t *CustomTracer) { t.isInKubernetes = isInKubernetes }
}

func SetCollectorHost(collectorHost string) Option {
	return func(t *CustomTracer) {
		if collectorHost == "" {
			t.logger.LogError("collectorHost cannot be empty for setting collector endpoint", errors.New("InvalidArgument: collectorHost cannot be empty"))
		} else {
			t.collectorHost = collectorHost
		}
	}
}

func SetTracingContext(ctx context.Context) Option {
	return func(t *CustomTracer) {
		if ctx == nil {
			t.logger.LogError("tracing context cannot be nil", errors.New("InvalidArgument: tracing context cannot be nil"))
		} else {
			t.traceContext = ctx
		}
	}
}

func SetSampler(sampler trace.Sampler) Option {
	return func(t *CustomTracer) {
		if sampler == nil {
			t.logger.LogError("sampler cannot be nil", errors.New("InvalidArgument: sampler cannot be nil"))
		} else {
			t.sampler = sampler
		}
	}
}

func SetPropagator(propagator propagation.TextMapPropagator) Option {
	return func(t *CustomTracer) {
		if propagator == nil {
			t.logger.LogError("propagator cannot be nil", errors.New("InvalidArgument: propagator cannot be nil"))
		} else {
			t.propagator = propagator
		}
	}
}

func SetOtelExporter(exporter *otlptrace.Exporter) Option {
	return func(t *CustomTracer) {
		if exporter == nil {
			t.logger.LogError("exporter cannot be nil", errors.New("InvalidArgument: exporter cannot be nil"))
		} else {
			t.exporter = exporter
		}
	}
}

func (c *CustomTracer) GetTextMapPropagator() propagation.TextMapPropagator {
	return c.propagator
}

func (c *CustomTracer) GetTracerProvider() *trace.TracerProvider {
	return c.traceProvider
}

func (c *CustomTracer) GetResource() *resource.Resource {
	return c.resource
}

func (c *CustomTracer) GetExporter() *otlptrace.Exporter {
	return c.exporter
}

func (c *CustomTracer) InitExporter() (*otlptrace.Exporter, error) {
	exporter, err := otlptracegrpc.New(c.traceContext, otlptracegrpc.WithEndpointURL("http://"+c.collectorHost+":4317"), otlptracegrpc.WithInsecure())
	if err != nil {
		c.logger.LogError("could not initialize otel exporter for tracing", err)
		return nil, err
	}
	c.exporter = exporter
	return exporter, nil
}

func (c *CustomTracer) InitResource() (*resource.Resource, error) {
	res, err := resource.New(c.traceContext, resource.WithAttributes(
		semconv.ServiceName(c.serviceName),
		semconv.OTelScopeName(otelgrpc.ScopeName),
		semconv.OTelScopeVersion(otelgrpc.Version()),
	))
	if err != nil {
		c.logger.LogError("could not set service name for tracing", err)
		return nil, err
	}
	c.resource = res
	return res, nil
}

func (c *CustomTracer) InitTracerProvider() (*trace.TracerProvider, error) {
	_, err := c.InitResource()
	if err != nil {
		return nil, err
	}
	_, err = c.InitExporter()
	if err != nil {
		return nil, err
	}
	provider := trace.NewTracerProvider(trace.WithResource(c.resource), trace.WithBatcher(c.exporter), trace.WithSampler(c.sampler))
	c.traceProvider = provider
	return provider, nil
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
	return customTracer
}

func (t *CustomTracer) Shutdown() {
	if t.traceProvider.TracerProvider != nil {
		t.traceProvider.Shutdown(t.traceContext)
	}
	if t.exporter != nil {
		t.exporter.Shutdown(t.traceContext)
	}
}
