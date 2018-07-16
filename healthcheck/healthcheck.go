package healthcheck

import (
	"context"
	"net"

	"github.com/carwale/golibraries/gologger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type healthCheckServer struct {
	healthCheckPort string
	checkFunction   func() (bool, error)
	logger          *gologger.CustomLogger
}

//Options sets the oprions for the health checking service
type Options func(hcs *healthCheckServer)

//Logger sets the logger for consul
//Defaults to consul logger
func Logger(customLogger *gologger.CustomLogger) Options {
	return func(hcs *healthCheckServer) { hcs.logger = customLogger }
}

// NewHealthCheckServer starts a health check server with the given port.
// It exposes a Check function that is compatible with consul
// The check function will call the 'checkFunction' that is passed and will return accordingly
func NewHealthCheckServer(healthCheckPort string, checkFunction func() (bool, error), options ...Options) bool {
	hcs := &healthCheckServer{
		healthCheckPort: healthCheckPort,
		checkFunction:   checkFunction,
	}

	for _, option := range options {
		option(hcs)
	}

	if hcs.logger == nil {
		hcs.logger = gologger.NewLogger()
	}

	return hcs.startHealthService()
}

func (hcs *healthCheckServer) Check(ctx context.Context, in *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	res, err := hcs.checkFunction()
	if err != nil {
		hcs.logger.LogError("Health Check failed with error", err)
		return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING}, nil
	}
	if !res {
		hcs.logger.LogErrorWithoutError("Health Check failed")
		return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func (hcs *healthCheckServer) startHealthService() bool {
	lis, err := net.Listen("tcp", hcs.healthCheckPort)
	if err != nil {
		hcs.logger.LogError("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(s, &healthCheckServer{})
	// Register reflection service on gRPC server.
	reflection.Register(s)
	if err := s.Serve(lis); err != nil {
		hcs.logger.LogError("failed to serve health service: %v", err)
		return false
	}
	return true
}
