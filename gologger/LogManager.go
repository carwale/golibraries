package gologger

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"gopkg.in/Graylog2/go-gelf.v2/gelf"
)

// CustomLogger is a graylog logger for golang
type CustomLogger struct {
	graylogHostName       string
	graylogPort           int
	graylogFacility       string
	logLevel              LogLevels
	isConsolePrintEnabled bool
	isTimeLoggingEnabled  bool
	disableGraylog        bool
	logger                *log.Logger
}

// Pair is a tuple of strings
type Pair struct {
	Key, Value string
}

// Option sets a parameter for the Logger
type Option func(l *CustomLogger)

// GraylogHost sets the graylog host for the logger. Default is 127.0.0.1
func GraylogHost(hostName string) Option {
	return func(l *CustomLogger) {
		if hostName != "" {
			l.graylogHostName = hostName
		}
	}
}

// GraylogPort sets the graylog port for the logger. Default is 11100
func GraylogPort(portNumber int) Option {
	return func(l *CustomLogger) {
		if portNumber != 0 {
			l.graylogPort = portNumber
		}
	}
}

// GraylogFacility sets the graylog facility for the logger. Default is "ErrorLogger"
func GraylogFacility(facility string) Option {
	return func(l *CustomLogger) {
		if facility != "" {
			l.graylogFacility = facility
		}
	}
}

// DisableGraylog disables graylog logging. Defaults to false
// If graylog is disabled console logging will be enabled by default
func DisableGraylog(flag bool) Option {
	return func(l *CustomLogger) { l.disableGraylog = flag }
}

// SetLogLevel sets the logger level Possible values are ERROR, WARN, INFO, DEBUG.
// Default is ERROR
func SetLogLevel(level string) Option {
	return func(l *CustomLogger) {
		switch level {
		case "ERROR":
			l.logLevel = ERROR
		case "WARN":
			l.logLevel = WARN
		case "INFO":
			l.logLevel = INFO
		case "DEBUG":
			fallthrough
		case "ALL":
			l.logLevel = DEBUG
		default:
			l.logLevel = ERROR
		}
	}

}

// ConsolePrintEnabled enables console output for logging. To be used only for development.
func ConsolePrintEnabled(flag bool) Option {
	return func(l *CustomLogger) { l.isConsolePrintEnabled = flag }
}

// TimeLoggingEnabled enables logging of time (The use of tic toc functions).
// This can be used in functions and then disabled here when there is no need.
func TimeLoggingEnabled(flag bool) Option {
	return func(l *CustomLogger) { l.isTimeLoggingEnabled = flag }
}

// NewLogger : returns a new logger. When no options are given, it returns an error logger
// With graylog logging as default to a port 11100 which is not in use. So it is prety much
// useless. Please provide graylog host and port at the very least.
func NewLogger(LoggerOptions ...Option) *CustomLogger {

	l := &CustomLogger{
		graylogHostName: "127.0.0.1",
		graylogPort:     11100,
		graylogFacility: "ErrorLogger",
		logLevel:        ERROR,
	}

	for _, option := range LoggerOptions {
		option(l)
	}

	graylogAddr := l.graylogHostName + ":" + strconv.Itoa(l.graylogPort)
	gelfWriter, err := gelf.NewUDPWriter(graylogAddr)
	if err != nil {
		log.Fatalf("gelf.NewWriter: %s", err)
	}
	// log to both stderr and graylog2
	if l.disableGraylog {
		l.logger = log.New(io.MultiWriter(os.Stderr), "", 0)
		l.logger.Printf("Logging to Stderr")
	} else if l.isConsolePrintEnabled {
		l.logger = log.New(io.MultiWriter(os.Stderr, gelfWriter), "", 0)
		l.logger.Printf("Logging to Stderr & Graylog @ %q", graylogAddr)
	} else {
		l.logger = log.New(io.MultiWriter(gelfWriter), "", 0)
		l.logger.Printf("Logging to Graylog @ %q", graylogAddr)
	}
	return l
}

// LogErrorInterface is used to send errors
func (l *CustomLogger) LogErrorInterface(v ...interface{}) {
	l.logger.Output(2, fmt.Sprint(v...))
}

// LogError is used to send errors and a message along with the error
func (l *CustomLogger) LogError(str string, err error) {
	l.logger.Printf(`{"log_level": %q, "log_timestamp": %q, "log_facility": %q,"log_message": %q,"log_error": %q}`,
		ERROR.String(), time.Now().String(), l.graylogFacility, str, err.Error())
}

// LogErrorWithoutError is used to send only a message and not an error
func (l *CustomLogger) LogErrorWithoutError(str string) {
	l.logMessage(str, ERROR)
}

// LogErrorWithoutErrorf is used to send only a message and not an error
func (l *CustomLogger) LogErrorWithoutErrorf(str string, args ...interface{}) {
	l.LogErrorWithoutError(fmt.Sprintf(str, args...))
}

//LogErrorMessage is used to send extra fields to graylog along with the error
func (l *CustomLogger) LogErrorMessage(str string, err error, pairs ...Pair) {
	if err != nil {
		pairs = append(pairs, Pair{"log_error", err.Error()})
	}
	l.logMessageWithExtras(str, ERROR, pairs)
}

// LogWarning is used to send warning messages
func (l *CustomLogger) LogWarning(str string) {
	if l.logLevel >= WARN {
		l.logMessage(str, WARN)
	}
}

// LogWarningf is used to send warning messages
func (l *CustomLogger) LogWarningf(str string, args ...interface{}) {
	l.LogWarning(fmt.Sprintf(str, args...))
}

//LogInfoMessage is used to send extra fields to graylog
func (l *CustomLogger) LogInfoMessage(str string, pairs ...Pair) {
	if l.logLevel >= INFO {
		l.logMessageWithExtras(str, INFO, pairs)
	}
}

// LogInfo is used to send info messages
func (l *CustomLogger) LogInfo(str string) {
	if l.logLevel >= INFO {
		l.logMessage(str, INFO)
	}
}

// LogInfof is used to send formatted info messages
func (l *CustomLogger) LogInfof(str string, args ...interface{}) {
	l.LogInfo(fmt.Sprintf(str, args...))
}

// LogDebug is used to send debug messages
func (l *CustomLogger) LogDebug(str string) {
	if l.logLevel >= DEBUG {
		l.logMessage(str, DEBUG)
	}
}

// LogDebugf is used to send debug messages
func (l *CustomLogger) LogDebugf(str string, args ...interface{}) {
	l.LogDebug(fmt.Sprintf(str, args...))
}

// LogMessage is used to log plain message
func (l *CustomLogger) LogMessage(message string) {
	l.logger.Printf(message)
}

// LogMessagef is used to log plain message
func (l *CustomLogger) LogMessagef(message string, args ...interface{}) {
	l.LogMessage(fmt.Sprintf(message, args...))
}

func (l *CustomLogger) logMessage(message string, level LogLevels) {
	l.logger.Printf(`{"log_level": %q, "log_timestamp": %q, "log_facility": %q,"log_message": %q}`,
		level.String(), time.Now().String(), l.graylogFacility, message)
}

func (l *CustomLogger) logMessageWithExtras(message string, level LogLevels, pairs []Pair) {
	pairs = append(pairs, Pair{"log_level", level.String()})
	pairs = append(pairs, Pair{"log_timestamp", time.Now().String()})
	pairs = append(pairs, Pair{"log_facility", l.graylogFacility})
	pairs = append(pairs, Pair{"log_message", message})
	var buffer bytes.Buffer
	buffer.WriteString("{")
	for index, pair := range pairs {
		buffer.WriteString(fmt.Sprintf("%q:%q", pair.Key, pair.Value))
		if index < len(pairs)-1 {
			buffer.WriteString(",")
		}
	}
	buffer.WriteString("}")

	l.logger.Print(buffer.String())

}

// Tic is used to log time taken by a function. It should be used along with Toc function
// Tic will take an input as a string message (It can be the name of the function)
// And will return time and the message. For full used see the Toc funtion
func (l *CustomLogger) Tic(s string) (string, time.Time) {
	return s, time.Now()
}

// Toc will log the time taken by the funtion. Its input is the output of the Tic function
// Here is an example code block for using Tic and Toc function
//	defer Toc(Tic("FunctionName"))
// This will the first line of the function
func (l *CustomLogger) Toc(message string, startTime time.Time) {
	if l.isTimeLoggingEnabled {
		endTime := time.Now()
		l.logger.Printf(`{"log_timestamp": %q, "log_timetaken": %q, "log_facility": %q,"log_message": %q}`,
			time.Now().String(), strconv.FormatInt(endTime.Sub(startTime).Nanoseconds(), 10), l.graylogFacility, message)
	}
}
