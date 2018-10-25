package gologger

import (
	"sync"
	"time"
)

// updatePacket : Struct that holds message updates
type updatePacket struct {
	identifier string
	labels     []string
	value      int64
}

// RateLatencyLogger : Logger that tracks multiple messages & prints to console
type RateLatencyLogger struct {
	messages     map[string]IMetricVec // Map that holds all module's messages
	updateTunnel chan updatePacket     // Channel which updates latency in message
	addMetric    chan updatePacket
	logger       *CustomLogger
	once         sync.Once
	isRan        bool
}

// Tic starts the timer
func (mgl *RateLatencyLogger) Tic() time.Time {
	return time.Now()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (mgl *RateLatencyLogger) Toc(start time.Time, identifier string, labels ...string) {
	if mgl.isRan {
		elapsed := int64(time.Since(start) / 1000)
		mgl.updateTunnel <- updatePacket{identifier, labels, elapsed}
	}
}

// Run : Starts the logger in a go routine.
// Calling this multiple times doesn't have any effect
func (mgl *RateLatencyLogger) Run() {
	mgl.once.Do(func() {
		go func() {
			for {
				select {
				case packet := <-mgl.updateTunnel:
					msg, ok := mgl.messages[packet.identifier]
					if !ok {
						mgl.logger.LogErrorWithoutError("wrong identifier passed. Could not find metric logger")
					}
					msg.Update(packet.value, packet.labels...)
				}
			}
		}()
		mgl.isRan = true
	})
}

// AddNewMetric sets New message initialisation function
func (mgl *RateLatencyLogger) AddNewMetric(messageIdentifier string, newMessage IMetricVec) {
	_, ok := mgl.messages[messageIdentifier]
	if !ok {
		mgl.messages[messageIdentifier] = newMessage
	}
}

// RateLatencyOption sets a parameter for the RateLatencyLogger
type RateLatencyOption func(rl *RateLatencyLogger)

// SetLogger sets the output logger.
// Default is stderr
func SetLogger(logger *CustomLogger) RateLatencyOption {
	return func(rl *RateLatencyLogger) {
		rl.logger = logger
	}
}

// NewRateLatencyLogger : returns a new RateLatencyLogger.
// When no options are given, it returns a RateLatencyLogger with default settings.
// Default logger is default custom logger.
func NewRateLatencyLogger(options ...RateLatencyOption) IMultiLogger {
	rl := &RateLatencyLogger{
		messages:     map[string]IMetricVec{},
		updateTunnel: make(chan updatePacket, 100),
		logger:       nil,
	}

	for _, option := range options {
		option(rl)
	}

	if rl.logger == nil {
		rl.logger = NewLogger()
	}

	return rl
}
