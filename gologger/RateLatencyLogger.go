package gologger

import (
	"sync"
	"time"
)

// updatePacket : Struct that holds message updates
type updatePacket struct {
	key   string
	value int64
}

// RateLatencyLogger : Logger that tracks multiple messages & prints to console
type RateLatencyLogger struct {
	interval     int                 // In seconds
	messages     map[string]IMessage // Map that holds all module's messages
	updateTunnel chan updatePacket   // Channel which updates latency in message
	logger       *CustomLogger
	newMessage   func(string) IMessage
	once         sync.Once
	isRan        bool
}

// Tic starts the timer
func (mgl *RateLatencyLogger) Tic(moduleName string) time.Time {
	return time.Now()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (mgl *RateLatencyLogger) Toc(moduleName string, start time.Time) {
	if mgl.isRan {
		elapsed := int64(time.Since(start) / 1000)
		mgl.updateTunnel <- updatePacket{moduleName, elapsed}
	}
}

// Push : Method to push the message to respective output stream (Console)
func (mgl *RateLatencyLogger) Push() {
	for _, m := range mgl.messages {
		msg := m.Jsonify()
		if msg != "" {
			go mgl.logger.LogMessage(msg)
		}
		m.Reset()
	}
}

// Run : Starts the logger in a go routine.
// Calling this multiple times doesn't have any effect
func (mgl *RateLatencyLogger) Run() {
	mgl.once.Do(func() {
		ticker := time.NewTicker(time.Duration(mgl.interval) * time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					mgl.Push()
				case packet := <-mgl.updateTunnel:
					msg, ok := mgl.messages[packet.key]
					if !ok {
						msg = mgl.newMessage(packet.key)
						mgl.messages[packet.key] = msg
					}
					msg.Update(packet.value)
				}
			}
		}()
		mgl.isRan = true
	})
}

// RateLatencyOption sets a parameter for the RateLatencyLogger
type RateLatencyOption func(rl *RateLatencyLogger)

// SetInterval sets the time interval to push the message.
// interval in seconds.
// Default is 60 seconds.
func SetInterval(interval int) RateLatencyOption {
	return func(rl *RateLatencyLogger) {
		if interval > 0 {
			rl.interval = interval
		}
	}
}

// SetNewMessage sets New message initialisation function
func SetNewMessage(newMessage func(string) IMessage) RateLatencyOption {
	return func(rl *RateLatencyLogger) {
		rl.newMessage = newMessage
	}
}

// SetMessages sets messages map
func SetMessages(modules []string) RateLatencyOption {
	return func(rl *RateLatencyLogger) {
		if rl.newMessage != nil {
			for _, m := range modules {
				rl.messages[m] = rl.newMessage(m)
			}
		}
	}
}

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
// NOTE: Be sure to SetNewMessage before setting option SetMessages.
func NewRateLatencyLogger(options ...RateLatencyOption) IMultiLogger {
	rl := &RateLatencyLogger{
		interval:     60,
		messages:     map[string]IMessage{},
		updateTunnel: make(chan updatePacket, 100),
		logger:       NewLogger(),
		newMessage:   NewMessage,
	}

	for _, option := range options {
		option(rl)
	}

	return rl
}

// NewMultiGoLogger initialises the RateLatencyLogger.
// This will be removed in next revision. Use NewRateLatencyLogger instead.
func NewMultiGoLogger(interval int, modules []string) IMultiLogger {
	return NewRateLatencyLogger(
		SetInterval(interval),
		SetMessages(modules),
		SetLogger(NewLogger(
			ConsolePrintEnabled(true),
			DisableGraylog(true),
		)),
	)
}

// NewMultiGrayLogger initialises the MultiGrayLogger.
// This will be removed in next revision. Use NewRateLatencyLogger instead.
func NewMultiGrayLogger(host string, port int, interval int, modules []string) IMultiLogger {
	return NewRateLatencyLogger(
		SetInterval(interval),
		SetMessages(modules),
		SetLogger(NewLogger(
			GraylogHost(host),
			GraylogPort(port),
			ConsolePrintEnabled(false),
		)),
	)
}
