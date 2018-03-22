package gologger

import (
	"fmt"
	"math"
	"time"
)

// Message : Default message type implementing IMessage
type Message struct {
	Requests     int
	TotalLatency int
	MaxLatency   int
	MinLatency   int
	Module       string // Module name
}

// Tic starts the timer
func (msg *Message) Tic() time.Time {
	return time.Now()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (msg *Message) Toc(start time.Time) int {
	elapsed := time.Since(start)
	return int(elapsed / 1000)
}

// Update the message with calculated latency
func (msg *Message) Update(elapsed int) {
	msg.Requests++
	latency := elapsed
	msg.TotalLatency += latency
	if latency < msg.MinLatency {
		msg.MinLatency = latency
	}
	if latency > msg.MaxLatency {
		msg.MaxLatency = latency
	}
}

// Jsonify : method to Jsonify the message struct
func (msg *Message) Jsonify() string {
	if msg.Requests <= 0 {
		return ""
	}
	meanLatency := 0
	meanLatency = msg.TotalLatency / msg.Requests
	minLatency := msg.MinLatency
	if minLatency == math.MaxInt32 {
		minLatency = 0
	}
	return fmt.Sprintf(`{"module": %q,"requestRate": %d,"meanLatency": %d,"maxLatency": %d,"minLatency": %d}`, msg.Module, msg.Requests, meanLatency, msg.MaxLatency, minLatency)
}

// Reset : Method to Reset the message struct
func (msg *Message) Reset() {
	msg.Requests = 0
	msg.TotalLatency = 0
	msg.MaxLatency = 0
	msg.MinLatency = math.MaxInt32
}
