package gologger

import(
	"time"
	"fmt"
	"math"
	"github.com/robertkowalski/graylog-golang"
)

type ILogger interface{
	Tic() time.Time
	Toc(time.Time)
	Push()
	Run()
}

type IMessage interface{
	Tic() time.Time
	Toc(time.Time)
	Jsonify() string
	Reset()
}

type Message struct{
	Requests, TotalLatency, MaxLatency, MinLatency int
	Source string		// Origin for the message (Server Name)
	Desc string			// Message description to be logged
}

func (msg *Message) Tic() time.Time{
	msg.Requests ++
	return time.Now()
}

func (msg *Message) Toc(start time.Time){
	elapsed := time.Since(start)
	latency := int(elapsed / 1000)
	msg.TotalLatency += latency
	if latency < msg.MinLatency {
		msg.MinLatency = latency
	}
	if latency > msg.MaxLatency {
		msg.MaxLatency = latency
	}
}

func (msg *Message) Jsonify() string{
	meanLatency := 0
	if msg.Requests > 0 {
		meanLatency = msg.TotalLatency/msg.Requests
	}
	minLatency := msg.MinLatency
	if minLatency == math.MaxInt32 {
		minLatency = 0
	}
	return fmt.Sprintf(`{"short_message": %q,
"source": %q,
"requestRate": %d,
"meanLatency": %d,
"maxLatency": %d,
"minLatency": %d
}`,msg.Desc,msg.Source,msg.Requests,meanLatency,msg.MaxLatency,minLatency)
}

func (msg *Message) Reset(){
	msg.Requests = 0
	msg.TotalLatency = 0
	msg.MaxLatency = 0
	msg.MinLatency = math.MaxInt32
}

type GoLogger struct{
	Interval	int 		// In seconds
	LogMessage 	IMessage
}

func (gl *GoLogger) Tic() time.Time{
	return gl.LogMessage.Tic()
}

func (gl *GoLogger) Toc(start time.Time){
	gl.LogMessage.Toc(start)
}

func (gl *GoLogger) Push(){
	fmt.Println(gl.LogMessage.Jsonify())
}

func (gl *GoLogger) Run(){
	ticker := time.NewTicker(time.Duration(gl.Interval) * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				gl.Push()
				gl.LogMessage.Reset()
			}
		}
	}()
}

type GrayLogger struct{
	*GoLogger
	GrayLog *gelf.Gelf
}

func (grl *GrayLogger) Push(){
	grl.GrayLog.Log(grl.GoLogger.LogMessage.Jsonify())
}

func (grl *GrayLogger) Run(){
	ticker := time.NewTicker(time.Duration(grl.GoLogger.Interval) * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				grl.Push()
				grl.GoLogger.LogMessage.Reset()
			}
		}
	}()
}

func NewGrayLogger(host string, port int, interval int, source string, desc string) ILogger{
	return &GrayLogger{GoLogger: &GoLogger{Interval: interval,
								LogMessage: &Message{Requests:0,
											TotalLatency:0,
											MaxLatency:0,
											MinLatency:math.MaxInt32,
											Source:source,
											Desc:desc}},
						GrayLog: gelf.New(gelf.Config{
									GraylogPort: port,
									GraylogHostname: host,
									Connection: "lan",
								})}
}

func NewGoLogger(interval int, source string, desc string) ILogger{
	return &GoLogger{Interval: interval,
					LogMessage: &Message{Requests:0,
										TotalLatency:0,
										MaxLatency:0,
										MinLatency:math.MaxInt32,
										Source:source,
										Desc:desc}}
}