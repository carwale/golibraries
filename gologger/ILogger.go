package gologger

import (
	"context"
	"time"
)

// ILogger defines the interface that all logger implementations must follow
type ILogger interface {
	// Basic logging methods
	LogError(str string, err error)
	LogErrorWithoutError(str string)
	LogErrorWithoutErrorf(str string, args ...interface{})
	LogErrorMessage(str string, err error, pairs ...Pair)

	LogWarning(str string)
	LogWarningf(str string, args ...interface{})
	LogWarningMessage(str string, pairs ...Pair)

	LogInfo(str string)
	LogInfof(str string, args ...interface{})
	LogInfoMessage(str string, pairs ...Pair)

	LogDebug(str string)
	LogDebugf(str string, args ...interface{})

	// Context-aware logging methods
	LogDebugWithContext(ctx context.Context, str string)
	LogDebugfWithContext(ctx context.Context, str string, args ...interface{})
	LogInfoWithContext(ctx context.Context, str string)
	LogInfofWithContext(ctx context.Context, str string, args ...interface{})
	LogWarningWithContext(ctx context.Context, str string)
	LogWarningfWithContext(ctx context.Context, str string, args ...interface{})
	LogErrorWithContext(ctx context.Context, str string, err error)
	LogErrorfWithContext(ctx context.Context, str string, err error, args ...interface{})

	// Utility methods
	LogMessage(message string)
	LogMessagef(message string, args ...interface{})
	LogMessageWithExtras(message string, level LogLevels, pairs ...Pair)
	LogErrorInterface(v ...interface{})

	// Time logging methods
	Tic(s string) (string, time.Time)
	Toc(message string, startTime time.Time)

	// Configuration methods
	GetLogLevel() LogLevels
}
