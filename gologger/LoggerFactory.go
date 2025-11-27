package gologger

// LoggerType defines the type of logger to create
type LoggerType string

const (
	// CustomLoggerType creates the original CustomLogger
	CustomLoggerType LoggerType = "custom"
	// ZerologLoggerType creates the new ZerologLogger
	ZerologLoggerType LoggerType = "zerolog"
)

// LoggerFactory creates loggers based on the specified type
type LoggerFactory struct{}

// NewLoggerFactory creates a new logger factory instance
func NewLoggerFactory() *LoggerFactory {
	return &LoggerFactory{}
}

// CreateCustomLogger creates a CustomLogger with the given options
func (f *LoggerFactory) CreateCustomLogger(options ...Option) ILogger {
	return NewLogger(options...)
}

// CreateZerologLogger creates a ZerologLogger with the given options
func (f *LoggerFactory) CreateZerologLogger(options ...ZerologOption) ILogger {
	return NewZerologLogger(options...)
}

// CreateLogger creates a logger of the specified type with common configuration
// This provides a unified way to create either logger type with similar settings
func (f *LoggerFactory) CreateLogger(loggerType LoggerType, config LoggerConfig) ILogger {
	switch loggerType {
	case CustomLoggerType:
		var options []Option

		if config.GraylogHost != "" {
			options = append(options, GraylogHost(config.GraylogHost))
		}
		if config.GraylogPort != 0 {
			options = append(options, GraylogPort(config.GraylogPort))
		}
		if config.GraylogFacility != "" {
			options = append(options, GraylogFacility(config.GraylogFacility))
		}
		if config.LogLevel != "" {
			options = append(options, SetLogLevel(config.LogLevel))
		}
		if config.K8sNamespace != "" {
			options = append(options, SetK8sNamespace(config.K8sNamespace))
		}
		if config.DisableGraylog {
			options = append(options, DisableGraylog(true))
		}
		if config.ConsolePrintEnabled {
			options = append(options, ConsolePrintEnabled(true))
		}
		if config.TimeLoggingEnabled {
			options = append(options, TimeLoggingEnabled(true))
		}

		return NewLogger(options...)

	case ZerologLoggerType:
		var options []ZerologOption

		if config.GraylogFacility != "" {
			options = append(options, WithFacility(config.GraylogFacility))
		}
		if config.LogLevel != "" {
			options = append(options, WithLogLevel(config.LogLevel))
		}
		if config.K8sNamespace != "" {
			options = append(options, WithK8sNamespace(config.K8sNamespace))
		}
		if config.TimeLoggingEnabled {
			options = append(options, WithTimeLogging(true))
		}

		if config.ConsolePrintEnabled {
			options = append(options, WithConsoleWriter())
		} else {
			// Default to JSON console output
			options = append(options, WithJSONConsole())
		}

		return NewZerologLogger(options...)

	default:
		// Default to CustomLogger for backward compatibility
		return NewLogger()
	}
}

// LoggerConfig holds common configuration for both logger types
type LoggerConfig struct {
	GraylogHost         string // Only used for CustomLogger
	GraylogPort         int    // Only used for CustomLogger
	GraylogFacility     string
	K8sNamespace        string
	LogLevel            string
	DisableGraylog      bool // Only used for CustomLogger
	ConsolePrintEnabled bool
	TimeLoggingEnabled  bool
}

// Convenience functions for quick logger creation

// NewCustomLoggerWithDefaults creates a CustomLogger with sensible defaults
func NewCustomLoggerWithDefaults() ILogger {
	return NewLogger(
		SetLogLevel("INFO"),
		ConsolePrintEnabled(true),
		DisableGraylog(true),
	)
}

// NewZerologLoggerWithDefaults creates a ZerologLogger with sensible defaults
func NewZerologLoggerWithDefaults() ILogger {
	return NewZerologLogger(
		WithLogLevel("INFO"),
		WithJSONConsole(),
	)
}

// NewProductionCustomLogger creates a CustomLogger configured for production
func NewProductionCustomLogger(graylogHost string, graylogPort int, facility string) ILogger {
	return NewLogger(
		GraylogHost(graylogHost),
		GraylogPort(graylogPort),
		GraylogFacility(facility),
		SetLogLevel("INFO"),
		ConsolePrintEnabled(false),
	)
}

// NewProductionZerologLogger creates a ZerologLogger configured for production
func NewProductionZerologLogger(facility string) ILogger {
	return NewZerologLogger(
		WithFacility(facility),
		WithLogLevel("INFO"),
		WithJSONConsole(),
	)
}

// NewDevelopmentCustomLogger creates a CustomLogger configured for development
func NewDevelopmentCustomLogger() ILogger {
	return NewLogger(
		SetLogLevel("DEBUG"),
		ConsolePrintEnabled(true),
		DisableGraylog(true),
		TimeLoggingEnabled(true),
	)
}

// NewDevelopmentZerologLogger creates a ZerologLogger configured for development
func NewDevelopmentZerologLogger() ILogger {
	return NewZerologLogger(
		WithLogLevel("DEBUG"),
		WithConsoleWriter(),
		WithTimeLogging(true),
	)
}
