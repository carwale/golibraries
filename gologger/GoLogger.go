package gologger

import (
	"time"
)

// IMultiLogger : Interface for Multi message logger
type IMultiLogger interface {
	// To measure time elapsed between any two points in the code,
	// Start the time logger by Tic(MessageDesc) and end the time logger by calling Toc(MessageDesc,time)
	Tic(string) time.Time
	Toc(string, time.Time)
	// Method to push the message to respective output stream (Console, Graylog, etc..)
	Push()
	// Starts the logger in a go routine
	Run()
}

// IMessage : Interface to implement for Message type
type IMessage interface {
	Update(int64)
	// Method to Jsonify the message struct
	Jsonify() string
	// Method to Reset the message struct
	Reset()
}
