package gologger

import (
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"gopkg.in/Graylog2/go-gelf.v2/gelf"
)

// LatencyPacket : Struct that holds latency details
type LatencyPacket struct {
	Module  string
	Latency int
}

// RateLatencyLogger : Logger that tracks multiple messages & prints to console
type RateLatencyLogger struct {
	interval     int                 // In seconds
	messages     map[string]IMessage // Map that holds all module's messages
	updateTunnel chan LatencyPacket  // Channel which updates latency in message
	logger       *log.Logger
	newMessage   func(string) IMessage
	once         sync.Once
	mux          sync.RWMutex // Mutex to lock before adding new message to messages map
	isRan        bool
}

// Tic starts the timer
func (mgl *RateLatencyLogger) Tic(moduleName string) time.Time {
	mgl.mux.RLock()
	msg, ok := mgl.messages[moduleName]
	mgl.mux.RUnlock()
	if ok {
		return msg.Tic()
	}
	// Lock to add new module to messages map
	mgl.mux.Lock()
	defer mgl.mux.Unlock()
	msg, ok = mgl.messages[moduleName]
	if !ok { // Double check
		msg = mgl.newMessage(moduleName)
		mgl.messages[moduleName] = msg
	}
	return msg.Tic()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (mgl *RateLatencyLogger) Toc(moduleName string, start time.Time) {
	if mgl.isRan {
		msg, ok := mgl.messages[moduleName]
		if ok {
			mgl.updateTunnel <- LatencyPacket{moduleName, msg.Toc(start)}
		}
	}
}

// Push : Method to push the message to respective output stream (Console)
func (mgl *RateLatencyLogger) Push() {
	for _, m := range mgl.messages {
		msg := m.Jsonify()
		if msg != "" {
			go mgl.logger.Println(msg)
		}
		m.Reset()
	}
}

// Run : Starts the logger in a go routine
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
					mgl.messages[packet.Module].Update(packet.Latency)
				}
			}
		}()
		mgl.isRan = true
	})
}

// InitialiseRateLatencyLogger with given input parameters
// msg : Messages map containing IMessages
// interval : frequency to push message to IOWriter
// updateTunnel : Channel which listens to incoming latency updates
// ioWriter : io.Writer stream to write the message
func InitialiseRateLatencyLogger(msgs map[string]IMessage, interval int, updateTunnel chan LatencyPacket, ioWriter io.Writer) IMultiLogger {
	return &RateLatencyLogger{
		interval:     interval,
		messages:     msgs,
		updateTunnel: updateTunnel,
		logger:       log.New(ioWriter, "", 0),
	}
}

// NewMultiGoLogger initialises the RateLatencyLogger
func NewMultiGoLogger(interval int, modules []string) IMultiLogger {
	logMessages := map[string]IMessage{}
	for _, m := range modules {
		logMessages[m] = &Message{
			Requests:     0,
			TotalLatency: 0,
			MaxLatency:   0,
			MinLatency:   math.MaxInt32,
			Module:       m,
		}
	}
	return InitialiseRateLatencyLogger(logMessages, interval, make(chan LatencyPacket, 100), os.Stderr)
}

// NewMultiGrayLogger initialises the MultiGrayLogger
func NewMultiGrayLogger(host string, port int, interval int, modules []string) IMultiLogger {
	graylogAddr := host + ":" + strconv.Itoa(port)
	gelfWriter, err := gelf.NewUDPWriter(graylogAddr)
	if err != nil {
		log.Fatalf("Cannot get gelf.NewWriter: %s", err)
	}
	logMessages := map[string]IMessage{}
	for _, m := range modules {
		logMessages[m] = &Message{
			Requests:     0,
			TotalLatency: 0,
			MaxLatency:   0,
			MinLatency:   math.MaxInt32,
			Module:       m,
		}
	}
	return InitialiseRateLatencyLogger(logMessages, interval, make(chan LatencyPacket, 100), gelfWriter)
}
