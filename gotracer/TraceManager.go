package gotracer

import (
	"context"
	"errors"

	"github.com/carwale/golibraries/gologger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
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
	tracerProvider, err := customTracer.initTracerProvider()
	if err != nil {
		customTracer.logger.LogError("error occurred while initializing tracer provider", err)
	}
	customTracer.traceProvider = tracerProvider
	return customTracer
}

func (t *CustomTracer) Shutdown() {
	if t.traceProvider.TracerProvider != nil {
		t.traceProvider.Shutdown(t.traceContext)
	} else {
		t.logger.LogError("could not shutdown traceprovider", errors.New("trace provider is nil"))
	}
	if t.exporter != nil {
		t.exporter.Shutdown(t.traceContext)
	} else {
		t.logger.LogError("could not shutdown exporter", errors.New("exporter is nil"))
	}
}
