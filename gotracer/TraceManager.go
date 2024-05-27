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

// CustomTracer struct holds the configuration and state for the tracing setup
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

// Option is a function type used to set various options for the CustomTracer
type Option func(t *CustomTracer)

// SetLogger sets the logger for the CustomTracer
func SetLogger(logger *gologger.CustomLogger) Option {
	return func(t *CustomTracer) { t.logger = logger }
}

// SetResource sets the resource for the CustomTracer
func SetResource(resource *resource.Resource) Option {
	return func(t *CustomTracer) {
		if resource == nil {
			t.logger.LogError("resource cannot be nil", errors.New("InvalidArgument: resource cannot be nil"))
		} else {
			t.resource = resource
		}
	}
}

// SetServiceName sets the service name for the CustomTracer
func SetServiceName(serviceName string) Option {
	return func(t *CustomTracer) {
		if serviceName == "" {
			t.logger.LogError("service name cannot be empty for tracing", errors.New("InvalidArgument: service name cannot be empty"))
		} else {
			t.serviceName = serviceName
		}
	}
}

// SetIsInKubernetes sets the Kubernetes environment flag for the CustomTracer
func SetIsInKubernetes(isInKubernetes bool) Option {
	return func(t *CustomTracer) { t.isInKubernetes = isInKubernetes }
}

// SetCollectorHost sets the collector host for the CustomTracer
func SetCollectorHost(collectorHost string) Option {
	return func(t *CustomTracer) {
		if collectorHost == "" {
			t.logger.LogError("collectorHost cannot be empty for setting collector endpoint", errors.New("InvalidArgument: collectorHost cannot be empty"))
		} else {
			t.collectorHost = collectorHost
		}
	}
}

// SetTracingContext sets the tracing context for the CustomTracer
func SetTracingContext(ctx context.Context) Option {
	return func(t *CustomTracer) {
		if ctx == nil {
			t.logger.LogError("tracing context cannot be nil", errors.New("InvalidArgument: tracing context cannot be nil"))
		} else {
			t.traceContext = ctx
		}
	}
}

// SetSampler sets the sampler for the CustomTracer
func SetSampler(sampler trace.Sampler) Option {
	return func(t *CustomTracer) {
		if sampler == nil {
			t.logger.LogError("sampler cannot be nil", errors.New("InvalidArgument: sampler cannot be nil"))
		} else {
			t.sampler = sampler
		}
	}
}

// SetPropagator sets the propagator for the CustomTracer
func SetPropagator(propagator propagation.TextMapPropagator) Option {
	return func(t *CustomTracer) {
		if propagator == nil {
			t.logger.LogError("propagator cannot be nil", errors.New("InvalidArgument: propagator cannot be nil"))
		} else {
			t.propagator = propagator
		}
	}
}

// SetOtelExporter sets the OpenTelemetry exporter for the CustomTracer
func SetOtelExporter(exporter *otlptrace.Exporter) Option {
	return func(t *CustomTracer) {
		if exporter == nil {
			t.logger.LogError("exporter cannot be nil", errors.New("InvalidArgument: exporter cannot be nil"))
		} else {
			t.exporter = exporter
		}
	}
}

// GetTextMapPropagator returns the propagator for the CustomTracer
func (c *CustomTracer) GetTextMapPropagator() propagation.TextMapPropagator {
	return c.propagator
}

// GetTracerProvider returns the tracer provider for the CustomTracer
func (c *CustomTracer) GetTracerProvider() *trace.TracerProvider {
	return c.traceProvider
}

// GetResource returns the resource for the CustomTracer
func (c *CustomTracer) GetResource() *resource.Resource {
	return c.resource
}

// GetExporter returns the exporter for the CustomTracer
func (c *CustomTracer) GetExporter() *otlptrace.Exporter {
	return c.exporter
}

// InitExporter initializes the OpenTelemetry exporter for tracing
func (c *CustomTracer) InitExporter() (*otlptrace.Exporter, error) {
	if c.collectorHost == "" {
		c.logger.LogError("collector host cannot be empty for setting collector endpoint", errors.New("InvalidArgument: collector host cannot be empty"))
		return nil, errors.New("InvalidArgument: collector host cannot be empty")
	}
	exporter, err := otlptracegrpc.New(c.traceContext, otlptracegrpc.WithEndpointURL("http://"+c.collectorHost+":4317"), otlptracegrpc.WithInsecure())
	if err != nil {
		c.logger.LogError("could not initialize otel exporter for tracing", err)
		return nil, err
	}
	if c.exporter == nil {
		c.exporter = exporter
	}
	c.exporter = exporter
	return exporter, nil
}

// InitResource initializes the OpenTelemetry resource for tracing
func (c *CustomTracer) InitResource() (*resource.Resource, error) {
	if c.serviceName == "" {
		c.logger.LogError("service name cannot be empty for tracing", errors.New("InvalidArgument: service name cannot be empty"))
		return nil, errors.New("InvalidArgument: service name cannot be empty")
	}
	res, err := resource.New(c.traceContext, resource.WithAttributes(
		semconv.ServiceName(c.serviceName),
		semconv.OTelScopeName(otelgrpc.ScopeName),
		semconv.OTelScopeVersion(otelgrpc.Version()),
	))
	if err != nil {
		c.logger.LogError("could not set service name for tracing", err)
		return nil, err
	}
	if c.resource == nil {
		c.resource = res
	}
	return res, nil
}

// InitTracerProvider initializes the OpenTelemetry tracer provider
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

// NewCustomTracer is the constructor for the CustomTracer struct
// It takes in a list of options to set various configuration options for the CustomTracer
// By default it sets a combination of parent based and trace id based sampler with 1% sampling rate
func NewCustomTracer(traceOptions ...Option) *CustomTracer {
	customTracer := &CustomTracer{
		sampler:      trace.ParentBased(trace.TraceIDRatioBased(0.01)),
		propagator:   propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
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

// Shutdown shuts down the tracer provider and exporter
func (t *CustomTracer) Shutdown() {
	if t.traceProvider != nil {
		t.traceProvider.Shutdown(t.traceContext)
	}
	if t.exporter != nil {
		t.exporter.Shutdown(t.traceContext)
	}
}
