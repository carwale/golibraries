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

// GoLogger : Default logger that prints to console
type GoLogger struct {
	Interval     int // In seconds
	LogMessage   IMessage
	UpdateTunnel chan int // Channel which updates latency in message
	Logger       *log.Logger
	once         sync.Once
	isRan        bool
}

// Tic starts the timer
func (gl *GoLogger) Tic() time.Time {
	return gl.LogMessage.Tic()
}

// Toc calculates the time elapsed since Tic() and stores in the Message
func (gl *GoLogger) Toc(start time.Time) {
	if gl.isRan {
		gl.UpdateTunnel <- gl.LogMessage.Toc(start)
	}
}

// Push : Method to push the message to respective output stream (Console)
func (gl *GoLogger) Push() {
	msg := gl.LogMessage.Jsonify()
	if msg != "" {
		go gl.Logger.Println(msg)
	}
	gl.LogMessage.Reset()
}

// Run : Starts the logger in a go routine
// Calling this multiple times doesn't have any effect
func (gl *GoLogger) Run() {
	gl.once.Do(func() {
		ticker := time.NewTicker(time.Duration(gl.Interval) * time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					gl.Push()
				case latency := <-gl.UpdateTunnel:
					gl.LogMessage.Update(latency)
				}
			}
		}()
		gl.isRan = true
	})
}

// InitialiseGoLogger with given input parameters
// msg : Message Struct implementing IMessage
// interval : frequency to push message to IOWriter
// updateTunnel : Channel which listens to incoming latency updates
// ioWriter : io.Writer stream to write the message
func InitialiseGoLogger(msg IMessage, interval int, updateTunnel chan int, ioWriter io.Writer) ILogger {
	return &GoLogger{
		Interval:     interval,
		LogMessage:   msg,
		UpdateTunnel: updateTunnel,
		Logger:       log.New(ioWriter, "", 0),
	}
}

// NewGoLogger initialises the default GoLogger
func NewGoLogger(interval int, module string) ILogger {
	msg := &Message{
		Requests:     0,
		TotalLatency: 0,
		MaxLatency:   0,
		MinLatency:   math.MaxInt32,
		Module:       module,
	}
	return InitialiseGoLogger(msg, interval, make(chan int, 100), os.Stderr)
}

// NewGrayLogger initialises the GrayLogger
func NewGrayLogger(host string, port int, interval int, module string) ILogger {
	graylogAddr := host + ":" + strconv.Itoa(port)
	gelfWriter, err := gelf.NewUDPWriter(graylogAddr)
	if err != nil {
		log.Fatalf("Cannot get gelf.NewWriter: %s", err)
	}
	msg := &Message{
		Requests:     0,
		TotalLatency: 0,
		MaxLatency:   0,
		MinLatency:   math.MaxInt32,
		Module:       module,
	}
	return InitialiseGoLogger(msg, interval, make(chan int, 100), gelfWriter)
}
