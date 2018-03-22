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

// MultiGoLogger : Logger that tracks multiple messages & prints to console
type MultiGoLogger struct {
	Interval     int                 // In seconds
	LogMessages  map[string]IMessage // Map that holds all module's messages
	UpdateTunnel chan LatencyPacket  // Channel which updates latency in message
	Logger       *log.Logger
	once         sync.Once
	mux          sync.Mutex // Mutex to lock before adding new message to LogMessages map
}

// Tic starts the timer
func (mgl *MultiGoLogger) Tic(moduleName string) time.Time {
	msg, ok := mgl.LogMessages[moduleName]
	if ok {
		return msg.Tic()
	}
	// Lock to add new module to messages map
	mgl.mux.Lock()
	defer mgl.mux.Unlock()
	mgl.LogMessages[moduleName] = &Message{
		Requests:     0,
		TotalLatency: 0,
		MaxLatency:   0,
		MinLatency:   math.MaxInt32,
		Module:       moduleName,
	}
	return mgl.LogMessages[moduleName].Tic()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (mgl *MultiGoLogger) Toc(moduleName string, start time.Time) {
	msg, ok := mgl.LogMessages[moduleName]
	if ok {
		mgl.UpdateTunnel <- LatencyPacket{moduleName, msg.Toc(start)}
	}
}

// Push : Method to push the message to respective output stream (Console)
func (mgl *MultiGoLogger) Push() {
	for _, m := range mgl.LogMessages {
		msg := m.Jsonify()
		if msg != "" {
			go mgl.Logger.Println(msg)
		}
		m.Reset()
	}
}

// Run : Starts the logger in a go routine
// Calling this multiple times doesn't have any effect
func (mgl *MultiGoLogger) Run() {
	mgl.once.Do(func() {
		ticker := time.NewTicker(time.Duration(mgl.Interval) * time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					mgl.Push()
				case packet := <-mgl.UpdateTunnel:
					mgl.LogMessages[packet.Module].Update(packet.Latency)
				}
			}
		}()
	})
}

// InitialiseMultiGoLogger with given input parameters
// msg : Messages map containing IMessages
// interval : frequency to push message to IOWriter
// updateTunnel : Channel which listens to incoming latency updates
// ioWriter : io.Writer stream to write the message
func InitialiseMultiGoLogger(msgs map[string]IMessage, interval int, updateTunnel chan LatencyPacket, ioWriter io.Writer) IMultiLogger {
	return &MultiGoLogger{
		Interval:     interval,
		LogMessages:  msgs,
		UpdateTunnel: updateTunnel,
		Logger:       log.New(ioWriter, "", 0),
	}
}

// NewMultiGoLogger initialises the MultiGoLogger
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
	return InitialiseMultiGoLogger(logMessages, interval, make(chan LatencyPacket, 100), os.Stderr)
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
	return InitialiseMultiGoLogger(logMessages, interval, make(chan LatencyPacket, 100), gelfWriter)
}
