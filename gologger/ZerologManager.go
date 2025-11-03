package gologger

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// ZerologLogger is a high-performance logger implementation using zerolog
type ZerologLogger struct {
	logger               zerolog.Logger
	logLevel             LogLevels
	facility             string
	k8sNamespace         string
	isTimeLoggingEnabled bool
}

// Ensure ZerologLogger implements ILogger interface
var _ ILogger = (*ZerologLogger)(nil)

// ZerologOption sets a parameter for the ZerologLogger
type ZerologOption func(l *ZerologLogger)

// WithFacility sets the log facility for the logger. Default is "ErrorLogger"
func WithFacility(facility string) ZerologOption {
	return func(l *ZerologLogger) {
		if facility != "" {
			l.facility = facility
		}
	}
}

// WithK8sNamespace sets the k8sNamespace for the logger. Default is "dev"
// This option will have no effect if env variable K8S_NAMESPACE is set
func WithK8sNamespace(k8sNamespace string) ZerologOption {
	return func(l *ZerologLogger) {
		if k8sNamespace != "" {
			l.k8sNamespace = k8sNamespace
		}
	}
}

// WithLogLevel sets the logger level. Possible values are ERROR, WARN, INFO, DEBUG.
// Default is ERROR
func WithLogLevel(level string) ZerologOption {
	return func(l *ZerologLogger) {
		switch level {
		case "ERROR":
			l.logLevel = ERROR
			l.logger = l.logger.Level(zerolog.ErrorLevel)
		case "WARN":
			l.logLevel = WARN
			l.logger = l.logger.Level(zerolog.WarnLevel)
		case "INFO":
			l.logLevel = INFO
			l.logger = l.logger.Level(zerolog.InfoLevel)
		case "DEBUG":
			fallthrough
		case "ALL":
			l.logLevel = DEBUG
			l.logger = l.logger.Level(zerolog.DebugLevel)
		default:
			l.logLevel = ERROR
			l.logger = l.logger.Level(zerolog.ErrorLevel)
		}
	}
}

// WithTimeLogging enables logging of time (The use of tic toc functions).
func WithTimeLogging(enabled bool) ZerologOption {
	return func(l *ZerologLogger) {
		l.isTimeLoggingEnabled = enabled
	}
}

// WithOutput sets the output writer for the logger
func WithOutput(writer io.Writer) ZerologOption {
	return func(l *ZerologLogger) {
		l.logger = l.logger.Output(writer)
	}
}

// WithConsoleWriter enables console-friendly output format with colors and human-readable timestamps
func WithConsoleWriter() ZerologOption {
	return func(l *ZerologLogger) {
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
			NoColor:    false,
		}
		l.logger = l.logger.Output(consoleWriter)
	}
}

// WithJSONConsole outputs structured JSON to console (default behavior)
func WithJSONConsole() ZerologOption {
	return WithOutput(os.Stdout)
}

// WithStderr outputs to standard error
func WithStderr() ZerologOption {
	return WithOutput(os.Stderr)
}

// NewZerologLogger creates a new high-performance logger using zerolog
func NewZerologLogger(options ...ZerologOption) *ZerologLogger {
	// Set up defaults
	l := &ZerologLogger{
		facility:     "ErrorLogger",
		logLevel:     ERROR,
		k8sNamespace: "dev",
	}

	// Check environment variable for K8s namespace
	if k8sNamespace, ok := os.LookupEnv("K8S_NAMESPACE"); ok && k8sNamespace != "" {
		l.k8sNamespace = k8sNamespace
	}

	// Initialize zerolog with default settings - JSON output to stdout
	l.logger = zerolog.New(os.Stdout).
		With().
		Timestamp().
		Str("log_facility", l.facility).
		Str("K8sNamespace", l.k8sNamespace).
		Logger().
		Level(zerolog.ErrorLevel)

	// Apply options
	for _, option := range options {
		option(l)
	}

	// Update logger with final facility and namespace values
	l.logger = l.logger.With().
		Str("log_facility", l.facility).
		Str("K8sNamespace", l.k8sNamespace).
		Logger()

	return l
}

// GetLogLevel returns the current log level
func (l *ZerologLogger) GetLogLevel() LogLevels {
	return l.logLevel
}

// LogErrorInterface logs errors with interface{} arguments
func (l *ZerologLogger) LogErrorInterface(v ...interface{}) {
	l.logger.Error().Msgf("%v", v...)
}

// LogError logs errors and a message along with the error
func (l *ZerologLogger) LogError(str string, err error) {
	l.logger.Error().Err(err).Msg(str)
}

// LogErrorWithoutError logs only a message without an error
func (l *ZerologLogger) LogErrorWithoutError(str string) {
	l.logger.Error().Msg(str)
}

// LogErrorWithoutErrorf logs only a formatted message without an error
func (l *ZerologLogger) LogErrorWithoutErrorf(str string, args ...interface{}) {
	l.logger.Error().Msgf(str, args...)
}

// LogErrorMessage logs extra fields to the log along with the error
func (l *ZerologLogger) LogErrorMessage(str string, err error, pairs ...Pair) {
	event := l.logger.Error()
	if err != nil {
		event = event.Err(err)
	}
	for _, pair := range pairs {
		event = event.Str(pair.Key, pair.Value)
	}
	event.Msg(str)
}

// LogWarning logs warning messages
func (l *ZerologLogger) LogWarning(str string) {
	if l.logLevel >= WARN {
		l.logger.Warn().Msg(str)
	}
}

// LogWarningf logs formatted warning messages
func (l *ZerologLogger) LogWarningf(str string, args ...interface{}) {
	if l.logLevel >= WARN {
		l.logger.Warn().Msgf(str, args...)
	}
}

// LogWarningMessage logs warning messages along with extra fields
func (l *ZerologLogger) LogWarningMessage(str string, pairs ...Pair) {
	if l.logLevel >= WARN {
		event := l.logger.Warn()
		for _, pair := range pairs {
			event = event.Str(pair.Key, pair.Value)
		}
		event.Msg(str)
	}
}

// LogInfoMessage logs extra fields
func (l *ZerologLogger) LogInfoMessage(str string, pairs ...Pair) {
	if l.logLevel >= INFO {
		event := l.logger.Info()
		for _, pair := range pairs {
			event = event.Str(pair.Key, pair.Value)
		}
		event.Msg(str)
	}
}

// LogInfo logs info messages
func (l *ZerologLogger) LogInfo(str string) {
	if l.logLevel >= INFO {
		l.logger.Info().Msg(str)
	}
}

// LogInfof logs formatted info messages
func (l *ZerologLogger) LogInfof(str string, args ...interface{}) {
	if l.logLevel >= INFO {
		l.logger.Info().Msgf(str, args...)
	}
}

// LogDebug logs debug messages
func (l *ZerologLogger) LogDebug(str string) {
	if l.logLevel >= DEBUG {
		l.logger.Debug().Msg(str)
	}
}

// LogDebugf logs formatted debug messages
func (l *ZerologLogger) LogDebugf(str string, args ...interface{}) {
	if l.logLevel >= DEBUG {
		l.logger.Debug().Msgf(str, args...)
	}
}

// LogMessage logs plain message
func (l *ZerologLogger) LogMessage(message string) {
	l.logger.Log().Msg(message)
}

// LogMessagef logs formatted plain message
func (l *ZerologLogger) LogMessagef(message string, args ...interface{}) {
	l.logger.Log().Msgf(message, args...)
}

// LogMessageWithExtras logs message with specified level and extra fields
func (l *ZerologLogger) LogMessageWithExtras(message string, level LogLevels, pairs ...Pair) {
	if l.logLevel >= level {
		var event *zerolog.Event
		switch level {
		case ERROR:
			event = l.logger.Error()
		case WARN:
			event = l.logger.Warn()
		case INFO:
			event = l.logger.Info()
		case DEBUG:
			event = l.logger.Debug()
		default:
			event = l.logger.Log()
		}

		for _, pair := range pairs {
			event = event.Str(pair.Key, pair.Value)
		}
		event.Msg(message)
	}
}

// Tic is used to log time taken by a function
func (l *ZerologLogger) Tic(s string) (string, time.Time) {
	return s, time.Now()
}

// Toc logs the time taken by the function
func (l *ZerologLogger) Toc(message string, startTime time.Time) {
	if l.isTimeLoggingEnabled {
		duration := time.Since(startTime)
		l.logger.Info().
			Dur("log_timetaken", duration).
			Int64("log_timetaken_ns", duration.Nanoseconds()).
			Msg(message)
	}
}

// addTraceContextToEvent adds OpenTelemetry trace context to a zerolog event
func (l *ZerologLogger) addTraceContextToEvent(ctx context.Context, event *zerolog.Event) *zerolog.Event {
	if ctx != nil {
		span := trace.SpanFromContext(ctx)
		if span.SpanContext().IsValid() {
			event = event.
				Str("trace_id", span.SpanContext().TraceID().String()).
				Str("span_id", span.SpanContext().SpanID().String()).
				Bool("is_trace_sampled", span.SpanContext().IsSampled())
		}
	}
	return event
}

// LogDebugWithContext logs debug messages with context
func (l *ZerologLogger) LogDebugWithContext(ctx context.Context, str string) {
	if l.logLevel >= DEBUG {
		event := l.addTraceContextToEvent(ctx, l.logger.Debug())
		event.Msg(str)
	}
}

// LogDebugfWithContext logs formatted debug messages with context
func (l *ZerologLogger) LogDebugfWithContext(ctx context.Context, str string, args ...interface{}) {
	if l.logLevel >= DEBUG {
		event := l.addTraceContextToEvent(ctx, l.logger.Debug())
		event.Msgf(str, args...)
	}
}

// LogInfoWithContext logs info messages with context
func (l *ZerologLogger) LogInfoWithContext(ctx context.Context, str string) {
	if l.logLevel >= INFO {
		event := l.addTraceContextToEvent(ctx, l.logger.Info())
		event.Msg(str)
	}
}

// LogInfofWithContext logs formatted info messages with context
func (l *ZerologLogger) LogInfofWithContext(ctx context.Context, str string, args ...interface{}) {
	if l.logLevel >= INFO {
		event := l.addTraceContextToEvent(ctx, l.logger.Info())
		event.Msgf(str, args...)
	}
}

// LogWarningWithContext logs warning messages with context
func (l *ZerologLogger) LogWarningWithContext(ctx context.Context, str string) {
	if l.logLevel >= WARN {
		event := l.addTraceContextToEvent(ctx, l.logger.Warn())
		event.Msg(str)
	}
}

// LogWarningfWithContext logs formatted warning messages with context
func (l *ZerologLogger) LogWarningfWithContext(ctx context.Context, str string, args ...interface{}) {
	if l.logLevel >= WARN {
		event := l.addTraceContextToEvent(ctx, l.logger.Warn())
		event.Msgf(str, args...)
	}
}

// LogErrorWithContext logs errors with context
func (l *ZerologLogger) LogErrorWithContext(ctx context.Context, str string, err error) {
	event := l.addTraceContextToEvent(ctx, l.logger.Error())
	if err != nil {
		event = event.Err(err)
	}
	event.Msg(str)
}

// LogErrorfWithContext logs formatted errors with context
func (l *ZerologLogger) LogErrorfWithContext(ctx context.Context, str string, err error, args ...interface{}) {
	event := l.addTraceContextToEvent(ctx, l.logger.Error())
	if err != nil {
		event = event.Err(err)
	}
	event.Msgf(str, args...)
}
