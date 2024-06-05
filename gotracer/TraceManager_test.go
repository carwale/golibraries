package gotracer

import (
	"context"
	"testing"

	"github.com/carwale/golibraries/gologger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

func TestNewCustomTracer(t *testing.T) {
	// Test case: NewCustomTracer returns nil when isInKubernetes is false
	logger := gologger.NewLogger()
	tracer := NewCustomTracer(SetLogger(logger), SetIsInKubernetes(false))
	if tracer != nil {
		t.Errorf("Expected NewCustomTracer to return nil when isInKubernetes is false")
	}

	// Test case: NewCustomTracer returns a non-nil CustomTracer when isInKubernetes is true
	tracer = NewCustomTracer(SetLogger(logger), SetIsInKubernetes(true))
	if tracer == nil {
		t.Errorf("Expected NewCustomTracer to return a non-nil CustomTracer when isInKubernetes is true")
	}
}

func TestSetters(t *testing.T) {
	logger := gologger.NewLogger()
	tracer := &CustomTracer{
		logger: logger,
	}

	// Test SetResource
	res := resource.NewSchemaless()
	SetResource(res)(tracer)
	if tracer.resource != res {
		t.Errorf("SetResource did not set the resource correctly")
	}

	// Test SetServiceName
	SetServiceName("test-service")(tracer)
	if tracer.serviceName != "test-service" {
		t.Errorf("SetServiceName did not set the service name correctly")
	}

	// Test SetCollectorHost
	SetCollectorHost("localhost:4317")(tracer)
	if tracer.collectorHost != "localhost:4317" {
		t.Errorf("SetCollectorHost did not set the collector host correctly")
	}

	// Test SetTracingContext
	ctx := context.Background()
	SetTracingContext(ctx)(tracer)
	if tracer.traceContext != ctx {
		t.Errorf("SetTracingContext did not set the tracing context correctly")
	}

	// Test SetSampler
	sampler := trace.AlwaysSample()
	SetSampler(sampler)(tracer)
	if tracer.sampler != sampler {
		t.Errorf("SetSampler did not set the sampler correctly")
	}

	// Test SetPropagator
	// propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{})
	// SetPropagator(propagator)(tracer)
	// if tracer.propagator != propagator {
	// 	t.Errorf("SetPropagator did not set the propagator correctly")
	// }
}

func TestInitExporter(t *testing.T) {
	logger := gologger.NewLogger()
	tracer := &CustomTracer{
		logger:        logger,
		collectorHost: "localhost:4317",
		traceContext:  context.Background(),
	}

	// Test successful exporter initialization
	exporter, err := tracer.InitExporter()
	if err != nil {
		t.Errorf("InitExporter failed: %v", err)
	}
	if exporter == nil {
		t.Errorf("InitExporter returned nil exporter")
	}

	// Test error case
	tracer.collectorHost = ""
	_, err = tracer.InitExporter()
	if err == nil {
		t.Errorf("InitExporter should have returned an error when collectorHost is empty")
	}
}

func TestInitResource(t *testing.T) {
	logger := gologger.NewLogger()
	tracer := &CustomTracer{
		logger:       logger,
		serviceName:  "test-service",
		traceContext: context.Background(),
	}

	// Test successful resource initialization
	res, err := tracer.InitResource()
	if err != nil {
		t.Errorf("InitResource failed: %v", err)
	}
	if res == nil {
		t.Errorf("InitResource returned nil resource")
	}

	// Test error case
	tracer.serviceName = ""
	_, err = tracer.InitResource()
	if err == nil {
		t.Errorf("InitResource should have returned an error when serviceName is empty")
	}
}

func TestInitTracerProvider(t *testing.T) {
	logger := gologger.NewLogger()
	tracer := &CustomTracer{
		logger:        logger,
		serviceName:   "test-service",
		collectorHost: "localhost:4317",
		sampler:       trace.AlwaysSample(),
		propagator:    propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}),
		traceContext:  context.Background(),
	}

	// Test successful tracer provider initialization
	provider, err := tracer.InitTracerProvider()
	if err != nil {
		t.Errorf("InitTracerProvider failed: %v", err)
	}
	if provider == nil {
		t.Errorf("InitTracerProvider returned nil provider")
	}

	// Test error case
	tracer.serviceName = ""
	_, err = tracer.InitTracerProvider()
	if err == nil {
		t.Errorf("InitTracerProvider should have returned an error when serviceName is empty")
	}
}
