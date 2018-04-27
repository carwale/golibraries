package gologger

import (
	"fmt"
	"math"
)

// Message : Default message type implementing IMessage
type Message struct {
	Requests     int64
	TotalLatency int64
	MaxLatency   int64
	MinLatency   int64
	Module       string // Module name
}

// Update the message with calculated latency
func (msg *Message) Update(elapsed int64) {
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
	meanLatency := msg.TotalLatency / msg.Requests
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

// NewMessage returns default message instance
func NewMessage(module string) IMessage {
	return &Message{
		Requests:     0,
		TotalLatency: 0,
		MaxLatency:   0,
		MinLatency:   math.MaxInt32,
		Module:       module,
	}
}
