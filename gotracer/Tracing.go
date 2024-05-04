package gotracer

import (
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func (customTracer *CustomTracer) initExporter() (*otlptrace.Exporter, error) {
	exporter, err := otlptracegrpc.New(customTracer.traceContext, otlptracegrpc.WithEndpointURL("http://"+customTracer.collectorHost+":4317"), otlptracegrpc.WithInsecure())
	if err != nil {
		customTracer.logger.LogError("could not initialize otel exporter for tracing", err)
		return nil, err
	}
	customTracer.exporter = exporter
	return exporter, nil
}

func (customTracer *CustomTracer) initResource() (*resource.Resource, error) {
	res, err := resource.New(customTracer.traceContext, resource.WithAttributes(semconv.ServiceName(customTracer.serviceName)))
	if err != nil {
		customTracer.logger.LogError("could not set service name for tracing", err)
		return nil, err
	}
	customTracer.resource = res
	return res, nil
}

func (customTracer *CustomTracer) initTracerProvider(enabled bool) error {
	res, err := customTracer.initResource()
	if err != nil {
		return err
	}
	exporter, err := customTracer.initExporter()
	if err != nil {
		return err
	}
	if enabled {
		customTracer.sampler = trace.AlwaysSample()
		provider := trace.NewTracerProvider(trace.WithResource(res), trace.WithBatcher(exporter), trace.WithSampler(customTracer.sampler))
		customTracer.traceProvider = provider
		customTracer.logger.LogInfo("tracing enabled")
	} else {
		customTracer.sampler = trace.NeverSample()
		provider := trace.NewTracerProvider(trace.WithSampler(customTracer.sampler))
		customTracer.traceProvider = provider
		customTracer.logger.LogInfo("tracing disabled")
	}
	return nil
}
