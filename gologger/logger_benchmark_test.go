package gologger

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Benchmark scenarios
const (
	logMessage     = "This is a test log message with some details about the operation"
	errorMessage   = "An error occurred during processing"
	benchmarkError = "database connection failed"
	traceID        = "1234567890abcdef1234567890abcdef"
	spanID         = "1234567890abcdef"
)

// Setup functions for different logger configurations

func setupCustomLogger() *CustomLogger {
	// Redirect to discard to avoid output interference
	return NewLogger(
		DisableGraylog(true),
		ConsolePrintEnabled(false),
		SetLogLevel("DEBUG"),
		GraylogFacility("BenchmarkLogger"),
	)
}

func setupZerologDiscard() zerolog.Logger {
	return zerolog.New(io.Discard).With().Timestamp().Logger()
}

func setupZerologBuffer() (zerolog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := zerolog.New(buf).With().Timestamp().Logger()
	return logger, buf
}

func setupZerologStderr() zerolog.Logger {
	return zerolog.New(os.Stderr).With().Timestamp().Logger()
}

// Benchmark basic logging operations

func BenchmarkCustomLogger_Info(b *testing.B) {
	logger := setupCustomLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfo(logMessage)
		}
	})
}

func BenchmarkZerolog_Info_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Msg(logMessage)
		}
	})
}

func BenchmarkZerolog_Info_Buffer(b *testing.B) {
	logger, _ := setupZerologBuffer()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Msg(logMessage)
		}
	})
}

// Benchmark formatted logging

func BenchmarkCustomLogger_Infof(b *testing.B) {
	logger := setupCustomLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfof("Processing request %d with status %s", 12345, "success")
		}
	})
}

func BenchmarkZerolog_Infof_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Msgf("Processing request %d with status %s", 12345, "success")
		}
	})
}

// Benchmark error logging

func BenchmarkCustomLogger_Error(b *testing.B) {
	logger := setupCustomLogger()
	err := errors.New(benchmarkError)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogError(errorMessage, err)
		}
	})
}

func BenchmarkZerolog_Error_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	err := errors.New(benchmarkError)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Error().Err(err).Msg(errorMessage)
		}
	})
}

// Benchmark logging with additional fields

func BenchmarkCustomLogger_InfoWithFields(b *testing.B) {
	logger := setupCustomLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfoMessage(logMessage,
				Pair{"user_id", "12345"},
				Pair{"request_id", "req-abc-123"},
				Pair{"endpoint", "/api/v1/users"},
				Pair{"duration_ms", "150"},
			)
		}
	})
}

func BenchmarkZerolog_InfoWithFields_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().
				Str("user_id", "12345").
				Str("request_id", "req-abc-123").
				Str("endpoint", "/api/v1/users").
				Str("duration_ms", "150").
				Msg(logMessage)
		}
	})
}

// Benchmark context logging (with tracing)

func createMockSpan() trace.Span {
	tracer := otel.Tracer("benchmark")
	ctx, span := tracer.Start(context.Background(), "benchmark-operation")
	_ = ctx
	return span
}

func BenchmarkCustomLogger_InfoWithContext(b *testing.B) {
	logger := setupCustomLogger()
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfoWithContext(ctx, logMessage)
		}
	})
}

func BenchmarkZerolog_InfoWithContext_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Ctx(ctx).Msg(logMessage)
		}
	})
}

// Benchmark different log levels (to test level checking overhead)

func BenchmarkCustomLogger_Debug_Disabled(b *testing.B) {
	// Logger with ERROR level (DEBUG disabled)
	logger := NewLogger(
		DisableGraylog(true),
		ConsolePrintEnabled(false),
		SetLogLevel("ERROR"),
	)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogDebug(logMessage) // This should be filtered out
		}
	})
}

func BenchmarkZerolog_Debug_Disabled(b *testing.B) {
	logger := setupZerologDiscard()
	logger = logger.Level(zerolog.ErrorLevel) // Only ERROR and above
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Debug().Msg(logMessage) // This should be filtered out
		}
	})
}

func BenchmarkCustomLogger_Debug_Enabled(b *testing.B) {
	logger := setupCustomLogger() // DEBUG enabled
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogDebug(logMessage)
		}
	})
}

func BenchmarkZerolog_Debug_Enabled(b *testing.B) {
	logger := setupZerologDiscard()
	logger = logger.Level(zerolog.DebugLevel)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Debug().Msg(logMessage)
		}
	})
}

// Benchmark complex logging scenarios

func BenchmarkCustomLogger_ComplexLog(b *testing.B) {
	logger := setupCustomLogger()
	err := errors.New(benchmarkError)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogErrorMessage(errorMessage, err,
				Pair{"user_id", "12345"},
				Pair{"request_id", "req-abc-123"},
				Pair{"endpoint", "/api/v1/users"},
				Pair{"method", "POST"},
				Pair{"status_code", "500"},
				Pair{"duration_ms", "1500"},
				Pair{"retry_count", "3"},
			)
		}
	})
}

func BenchmarkZerolog_ComplexLog_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	err := errors.New(benchmarkError)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Error().
				Err(err).
				Str("user_id", "12345").
				Str("request_id", "req-abc-123").
				Str("endpoint", "/api/v1/users").
				Str("method", "POST").
				Str("status_code", "500").
				Str("duration_ms", "1500").
				Str("retry_count", "3").
				Msg(errorMessage)
		}
	})
}

// Benchmark time logging (Tic/Toc vs duration)

func BenchmarkCustomLogger_TicToc(b *testing.B) {
	logger := NewLogger(
		DisableGraylog(true),
		ConsolePrintEnabled(false),
		TimeLoggingEnabled(true),
		SetLogLevel("DEBUG"),
	)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			func() {
				defer logger.Toc(logger.Tic("benchmark-operation"))
				// Simulate some work
				time.Sleep(time.Microsecond)
			}()
		}
	})
}

func BenchmarkZerolog_Duration_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			start := time.Now()
			// Simulate some work
			time.Sleep(time.Microsecond)
			logger.Info().
				Dur("duration", time.Since(start)).
				Msg("benchmark-operation")
		}
	})
}

// Memory allocation benchmarks

func BenchmarkCustomLogger_Allocations(b *testing.B) {
	logger := setupCustomLogger()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.LogInfoMessage(logMessage,
			Pair{"key1", "value1"},
			Pair{"key2", "value2"},
		)
	}
}

func BenchmarkZerolog_Allocations_Discard(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info().
			Str("key1", "value1").
			Str("key2", "value2").
			Msg(logMessage)
	}
}

// Benchmark with different outputs (to test I/O impact)

func BenchmarkCustomLogger_WithFile(b *testing.B) {
	// Create a temporary file for output
	tmpFile, err := os.CreateTemp("", "benchmark_log_*.log")
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	logger := setupCustomLogger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfo(logMessage)
		}
	})
}

func BenchmarkZerolog_WithFile(b *testing.B) {
	// Create a temporary file for output
	tmpFile, err := os.CreateTemp("", "benchmark_log_*.log")
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	logger := zerolog.New(tmpFile).With().Timestamp().Logger()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Msg(logMessage)
		}
	})
}

// Comparative benchmarks (direct comparison)

func BenchmarkComparison_SimpleInfo(b *testing.B) {
	customLogger := setupCustomLogger()
	zerologLogger := setupZerologDiscard()

	b.Run("CustomLogger", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				customLogger.LogInfo(logMessage)
			}
		})
	})

	b.Run("Zerolog", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				zerologLogger.Info().Msg(logMessage)
			}
		})
	})
}

// Interface-based benchmarks (using the new interface pattern)

func BenchmarkInterface_SimpleInfo(b *testing.B) {
	// Create loggers through interface
	customLogger := NewLogger(
		DisableGraylog(true),
		ConsolePrintEnabled(false),
		SetLogLevel("DEBUG"),
		GraylogFacility("BenchmarkLogger"),
	)

	zerologLogger := NewZerologLogger(
		WithStderr(),
		WithLogLevel("DEBUG"),
		WithFacility("BenchmarkLogger"),
	)

	b.Run("CustomLogger_Interface", func(b *testing.B) {
		benchmarkLoggerInterface(b, customLogger)
	})

	b.Run("ZerologLogger_Interface", func(b *testing.B) {
		benchmarkLoggerInterface(b, zerologLogger)
	})
}

func BenchmarkInterface_StructuredLogging(b *testing.B) {
	customLogger := NewLogger(DisableGraylog(true), SetLogLevel("DEBUG"))
	zerologLogger := NewZerologLogger(WithStderr(), WithLogLevel("DEBUG"))

	b.Run("CustomLogger_Structured", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				customLogger.LogInfoMessage(logMessage,
					Pair{"user_id", "12345"},
					Pair{"request_id", "req-abc-123"},
				)
			}
		})
	})

	b.Run("ZerologLogger_Structured", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				zerologLogger.LogInfoMessage(logMessage,
					Pair{"user_id", "12345"},
					Pair{"request_id", "req-abc-123"},
				)
			}
		})
	})
}

// Helper function for interface-based benchmarking
func benchmarkLoggerInterface(b *testing.B, logger ILogger) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.LogInfo(logMessage)
		}
	})
}

func BenchmarkComparison_WithFields(b *testing.B) {
	customLogger := setupCustomLogger()
	zerologLogger := setupZerologDiscard()

	b.Run("CustomLogger", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				customLogger.LogInfoMessage(logMessage,
					Pair{"user_id", "12345"},
					Pair{"request_id", "req-abc-123"},
				)
			}
		})
	})

	b.Run("Zerolog", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				zerologLogger.Info().
					Str("user_id", "12345").
					Str("request_id", "req-abc-123").
					Msg(logMessage)
			}
		})
	})
}

// Benchmark global logger vs instance logger

func BenchmarkZerolog_GlobalLogger(b *testing.B) {
	// Use global zerolog logger
	log.Logger = log.Output(io.Discard)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			log.Info().Msg(logMessage)
		}
	})
}

func BenchmarkZerolog_InstanceLogger(b *testing.B) {
	logger := setupZerologDiscard()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().Msg(logMessage)
		}
	})
}
