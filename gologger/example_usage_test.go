package gologger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/carwale/golibraries/gologger"
)

// Example_usage demonstrates how to use the interface pattern to switch between logger implementations
func Example_usage() {
	// Method 1: Direct constructor calls (existing approach)

	// Create CustomLogger (original implementation)
	customLogger := gologger.NewLogger(
		gologger.SetLogLevel("INFO"),
		gologger.ConsolePrintEnabled(true),
		gologger.DisableGraylog(true),
	)

	// Create ZerologLogger (new high-performance implementation)
	zerologLogger := gologger.NewZerologLogger(
		gologger.WithLogLevel("INFO"),
		gologger.WithConsoleWriter(),
	)

	// Method 2: Using the factory pattern
	factory := gologger.NewLoggerFactory()

	// Create CustomLogger using factory
	factoryCustomLogger := factory.CreateCustomLogger(
		gologger.SetLogLevel("INFO"),
		gologger.DisableGraylog(true),
	)

	// Create ZerologLogger using factory
	factoryZerologLogger := factory.CreateZerologLogger(
		gologger.WithLogLevel("INFO"),
		gologger.WithConsoleWriter(),
	)

	// Method 3: Using unified configuration
	config := gologger.LoggerConfig{
		LogLevel:            "INFO",
		DisableGraylog:      true,
		ConsolePrintEnabled: true,
	}

	// Create either logger type with same config
	unifiedCustomLogger := factory.CreateLogger(gologger.CustomLoggerType, config)
	unifiedZerologLogger := factory.CreateLogger(gologger.ZerologLoggerType, config)

	// Method 4: Using convenience functions
	devCustomLogger := gologger.NewDevelopmentCustomLogger()
	devZerologLogger := gologger.NewDevelopmentZerologLogger()

	// All loggers implement the same interface
	testLogger(customLogger)
	testLogger(zerologLogger)
	testLogger(factoryCustomLogger)
	testLogger(factoryZerologLogger)
	testLogger(unifiedCustomLogger)
	testLogger(unifiedZerologLogger)
	testLogger(devCustomLogger)
	testLogger(devZerologLogger)
}

// testLogger demonstrates that both implementations work identically through the interface
func testLogger(logger gologger.ILogger) {
	// Basic logging
	logger.LogInfo("This is an info message")
	logger.LogWarning("This is a warning message")
	logger.LogError("This is an error message", errors.New("sample error"))

	// Formatted logging
	logger.LogInfof("User %s logged in from IP %s", "john_doe", "192.168.1.1")
	logger.LogErrorWithoutErrorf("Invalid request: status %d", 400)

	// Structured logging
	logger.LogInfoMessage("User action completed",
		gologger.Pair{"user_id", "12345"},
		gologger.Pair{"action", "login"},
		gologger.Pair{"duration_ms", "150"},
	)

	// Context logging
	ctx := context.Background()
	logger.LogInfoWithContext(ctx, "Processing request with trace context")

	// Time measurement
	defer logger.Toc(logger.Tic("example_operation"))

	// Level checking
	if logger.GetLogLevel() >= gologger.INFO {
		logger.LogDebug("Debug information")
	}
}

// ExampleServiceLogger shows how to use the logger in a service
type ExampleService struct {
	logger gologger.ILogger
	name   string
}

func NewExampleService(logger gologger.ILogger, name string) *ExampleService {
	return &ExampleService{
		logger: logger,
		name:   name,
	}
}

func (s *ExampleService) ProcessRequest(ctx context.Context, userID string) error {
	defer s.logger.Toc(s.logger.Tic("ProcessRequest"))

	s.logger.LogInfoWithContext(ctx, "Processing request started")

	// Simulate some processing
	if userID == "" {
		err := errors.New("user ID cannot be empty")
		s.logger.LogErrorWithContext(ctx, "Invalid request", err)
		return err
	}

	s.logger.LogInfoMessage("Request processed successfully",
		gologger.Pair{"service", s.name},
		gologger.Pair{"user_id", userID},
	)

	return nil
}

// Example_migration shows how to gradually migrate from CustomLogger to ZerologLogger
func Example_migration() {
	// Step 1: Extract logger creation into a function
	createLogger := func(useZerolog bool) gologger.ILogger {
		if useZerolog {
			return gologger.NewZerologLogger(
				gologger.WithLogLevel("INFO"),
			)
		}
		return gologger.NewLogger(
			gologger.SetLogLevel("INFO"),
			gologger.DisableGraylog(true),
		)
	}

	// Step 2: Use feature flag or environment variable to control logger type
	useZerolog := false // This could come from config/environment
	logger := createLogger(useZerolog)

	// Step 3: Use the logger through the interface
	service := NewExampleService(logger, "migration-example")

	ctx := context.Background()
	if err := service.ProcessRequest(ctx, "user123"); err != nil {
		logger.LogError("Service request failed", err)
	}
}

// Benchmark comparison - you can switch the logger type to compare performance
func BenchmarkLoggerInterface(b *testing.B) {
	// Test with CustomLogger
	b.Run("CustomLogger", func(b *testing.B) {
		logger := gologger.NewLogger(gologger.DisableGraylog(true))
		benchmarkLogger(b, logger)
	})

	// Test with ZerologLogger
	b.Run("ZerologLogger", func(b *testing.B) {
		logger := gologger.NewZerologLogger()
		benchmarkLogger(b, logger)
	})
}

func benchmarkLogger(b *testing.B, logger gologger.ILogger) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfo("This is a benchmark test message")
		}
	})
}
