package zipkinmanager

import (
	"errors"
	"strconv"
	"sync"

	"github.com/carwale/golibraries/gologger"

	"github.com/carwale/golibraries/goutilities"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	zipkin "github.com/openzipkin/zipkin-go-opentracing"
	"github.com/openzipkin/zipkin-go-opentracing/types"
	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"
)

const (
	sameSpan      = true
	traceID128Bit = true
	//TraceID is the constant that is used by all zipkin libraries accross for tracing
	TraceID = "traceid"
	//SpanID is the constant that is used by all zipkin libraries accross for tracing
	SpanID = "spanid"
	//ParentSpanID is the constant that is used by all zipkin libraries accross for tracing
	ParentSpanID = "pid"
	//IsSampled is the constant that is used by all zipkin libraries accross for tracing
	IsSampled = "issampled"
)

var once sync.Once

//ZipkinTracer is the structure that holds zipkin related information
type ZipkinTracer struct {
	logger             *gologger.CustomLogger
	isDebug            bool
	serviceName        string
	zipkinHTTPEndpoint string
	rabbitMQServers    []string
	isZipkinActive     bool
	isRabbitmqActive   bool
}

var z *ZipkinTracer

//Options sets options for zipkin tracer
type Options func(z *ZipkinTracer)

//SetServiceName will set the name of the application is zipkin
//should be used. else zipkin will be shown as name of the application
//Defaults to zipkin
func SetServiceName(name string) Options {
	return func(z *ZipkinTracer) {
		if name != "" {
			z.serviceName = name
		}
	}
}

//SetZipkinHTTPEndPoint will set the zipkin endpoint
//Defaults to "http://127.0.0.1:/api/v1/spans"
func SetZipkinHTTPEndPoint(endPoint string) Options {
	return func(z *ZipkinTracer) {
		if endPoint != "" {
			z.zipkinHTTPEndpoint = endPoint
		}
	}
}

//SetRabbitMqServers will set the servers for rabbitmq server
//This options should be given.
//Defaults to localhost
func SetRabbitMqServers(servers []string) Options {
	return func(z *ZipkinTracer) {
		if len(servers) != 0 {
			z.rabbitMQServers = servers
		}
	}
}

//Logger sets the logger for consul
//Defaults to consul logger
func Logger(customLogger *gologger.CustomLogger) Options {
	return func(z *ZipkinTracer) { z.logger = customLogger }
}

//NewZipkinTracer returns a zipkin tracer object.
//This is a singleton function, so will return the same instance of tracer if
//called multiple times. And also there will be no effect of options sent if called
//again.
func NewZipkinTracer(options ...Options) *ZipkinTracer {

	once.Do(func() {
		z = &ZipkinTracer{
			serviceName:        "zipkin",
			zipkinHTTPEndpoint: "http://127.0.0.1:/api/v1/spans",
			rabbitMQServers:    []string{"127.0.0.1"},
			isRabbitmqActive:   true,
			isZipkinActive:     true,
		}

		for _, option := range options {
			option(z)
		}

		if z.logger == nil {
			z.logger = gologger.NewLogger()
		}
		collector, err := NewRabbitMQCollector(z.rabbitMQServers, RabbitMQLogger(z.logger))
		if err != nil {
			z.logger.LogError("could not create rabbitmq collector!!", err)
			z.isZipkinActive = false
			z.isRabbitmqActive = false
		}
		recorder := zipkin.NewRecorder(collector, z.isDebug, "0.0.0.0:0", z.serviceName)

		tracer, err := zipkin.NewTracer(
			recorder,
			zipkin.ClientServerSameSpan(sameSpan),
			zipkin.TraceID128Bit(traceID128Bit),
		)
		if err != nil {
			z.logger.LogError("Unable to Create tracer ", err)
			z.isZipkinActive = false
		}

		opentracing.SetGlobalTracer(tracer)
	})
	return z
}

//GetSpanFromContext gets the span details from the context
//It assumes that "traceid", "spanid", "pid", "issampled" is set in the context
func (z *ZipkinTracer) GetSpanFromContext(ctx context.Context, spanName string) opentracing.Span {

	traceID, spanID, pid, err := z.getIdsFromContext(ctx)
	if err != nil {
		z.logger.LogError("Could not get IDs from context", err)
		return nil
	}
	myctx := zipkin.SpanContext{
		Sampled:      true,
		SpanID:       spanID,
		TraceID:      traceID,
		ParentSpanID: pid,
	}
	span := opentracing.GlobalTracer().StartSpan(spanName, ext.RPCServerOption(myctx), ext.SpanKindRPCServer)
	return span
}

func (z *ZipkinTracer) getChaildSpanFromContext(ctx context.Context, spanName string) (opentracing.Span, uint64, uint64) {
	traceID, spanID, _, err := z.getIdsFromContext(ctx)
	if err != nil {
		z.logger.LogError("Unable to get Child span From Context ", err)
		return nil, 0, 0
	}
	var newSpanID = goutilities.RandomUint64()
	myctx := zipkin.SpanContext{
		Sampled:      true,
		SpanID:       newSpanID,
		TraceID:      traceID,
		ParentSpanID: &spanID,
	}

	span := opentracing.GlobalTracer().StartSpan("let it be something", ext.RPCServerOption(myctx), ext.SpanKindRPCServer)
	return span, newSpanID, spanID
}

func (z *ZipkinTracer) getIdsFromContext(ctx context.Context) (types.TraceID, uint64, *uint64, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		z.logger.LogErrorWithoutError("Could not get any Id from context")
		var tmp uint64
		return types.TraceID{}, 0, &tmp, errors.New("Could not get any Id from context")
	}
	traceID := md[TraceID][0]
	traceIDInt, err := strconv.ParseUint(traceID, 10, 64)
	if err != nil {
		z.logger.LogError("could not get trace id ", err)
		var tmp uint64
		return types.TraceID{}, 0, &tmp, err
	}
	spanid, err := strconv.ParseUint(md[SpanID][0], 10, 64)
	if err != nil {
		z.logger.LogError("Could not get span Id from context", err)
		var tmp uint64
		return types.TraceID{}, 0, &tmp, err
	}
	pid, err := strconv.ParseUint(md[ParentSpanID][0], 10, 64)
	if err != nil {
		z.logger.LogError("Could not get parent span Id from context", err)
		var tmp uint64
		return types.TraceID{}, 0, &tmp, err
	}

	return types.TraceID{Low: traceIDInt, High: 0}, spanid, &pid, nil

}

//Getstatus checks if the grpc call is sampled or not
//It uses the issampled field to check
func (z *ZipkinTracer) Getstatus(ctx context.Context) bool {
	if !z.isRabbitmqActive || !z.isZipkinActive {
		return false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		z.logger.LogErrorWithoutError("Could not get any Id from context")
		return false
	}
	statusList := md[IsSampled]
	if statusList == nil {
		return false
	}
	status := statusList[0]
	if status == "true" {
		return true
	}
	return false

}

//CreateContextAndSpan creats context from span.
//It will inject "traceid", "spanid", "pid", "issampled" into the context
func (z *ZipkinTracer) CreateContextAndSpan(ctx context.Context, st string) (opentracing.Span, context.Context) {
	var traceIDInt, pid, spanid uint64
	var span opentracing.Span
	span, spanid, pid = z.getChaildSpanFromContext(ctx, st)
	span.LogEvent("client_send")

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		z.logger.LogErrorWithoutError("Could not get any Id from context")
	}
	traceID := md[TraceID][0]
	traceIDInt, err := strconv.ParseUint(traceID, 10, 64)
	if err != nil {
		z.logger.LogError("Could not get trace id ", err)
	}

	span.SetOperationName(st)
	ctx = metadata.NewOutgoingContext(context.Background(), metadata.Pairs(SpanID, strconv.FormatUint(spanid, 10), TraceID,
		strconv.FormatUint(traceIDInt, 10), ParentSpanID, strconv.FormatUint(pid, 10), IsSampled, "true"))
	return span, ctx
}
