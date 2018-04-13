package gologger

// LogLevels are the log levels for logging
type LogLevels uint32

//go:generate stringer -type=LogLevels

const (
	// ERROR : All errors will be logged with this level
	ERROR LogLevels = 0

	// WARN : All important events should be logged with a warn
	WARN LogLevels = 1

	// INFO : All events other than the important ones to be logged here
	INFO LogLevels = 2

	// DEBUG : This is for debug purposes only. Never use it on staging and production
	DEBUG LogLevels = 3
)
