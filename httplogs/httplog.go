package httplogs

import (
	"bytes"
	"fmt"
	"net/http"
	// "strconv"
	"time"

	objConsulAgent "github.com/carwale/golibraries/consulagent"
	"github.com/carwale/golibraries/gologger"
)

var (
	globalConsulAgent      *objConsulAgent.ConsulAgent
	isMonitoringLogEnabled bool
	serviceLogger          *gologger.CustomLogger
	globalserviceName      string
)

type BhriguResponseHeader struct {
	superHandler http.Handler
}

// InitLoggingWrapper acts as a constructor to initialize the logging service and
// initailize the struct
func InitLoggingWrapper(handlerToWrap http.Handler, key string, consulIP string, logger *gologger.CustomLogger, serviceName string) *BhriguResponseHeader {
	// fmt.Println("httplog.Constructor called")
	setBasicConfig(key, consulIP, logger, serviceName)
	return &BhriguResponseHeader{handlerToWrap}
}

//ServeHTTP acts as middleware and can be used to do pre/post processing
func (rh *BhriguResponseHeader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// fmt.Println("httplog.ServeHTTP called")

	//call the wrapped handler
	rh.superHandler.ServeHTTP(w, r)
}

// SetBasicConfig start point of the request
func setBasicConfig(key string, consulIP string, logger *gologger.CustomLogger, serviceName string) {
	// fmt.Println("httplog.setBasicConfig called")
	globalConsulAgent = objConsulAgent.NewConsulAgent(
		objConsulAgent.ConsulHost(consulIP),
		objConsulAgent.Logger(logger),
	)
	serviceLogger = logger
	globalserviceName = serviceName

	go checkHTTPLogStatus(key)
}

func checkHTTPLogStatus(key string) {
	for {
		// fmt.Println("**************isMonitoringLogEnabled:" + strconv.FormatBool(isMonitoringLogEnabled))
		time.Sleep(10 * time.Second)

		// Monitoring key considered here
		bhriguLogger := getValueFromConsulByKey(key)
		loggerTime, err := time.Parse("01/02/2006 15:04:05", bhriguLogger)

		if err != nil {
			isMonitoringLogEnabled = false
		}

		if loggerTime.Before(time.Now()) {
			isMonitoringLogEnabled = false
		}

		isMonitoringLogEnabled = true
	}
}

// LogHTTPLogs display the log based on isMonitoringLogEnabled flag
func LogHTTPLogs(r *http.Request, statusCode int) {
	if !isMonitoringLogEnabled {
		return
	}

	httpLog := []gologger.Pair{
		{Key: "time_iso8601", Value: time.Now().Format(time.RFC3339)},
		{Key: "proxyUpstreamName", Value: globalserviceName},
		{Key: "upstreamStatus", Value: fmt.Sprintf("%d", statusCode)},
		{Key: "upstream", Value: getIP(r)},
		{Key: "request_method", Value: r.Method},
		{Key: "request_uri", Value: getAbsoluteUrl(r)},
		{Key: "status", Value: fmt.Sprintf("%d", statusCode)},
		// {Key: "request_length", Value: fmt.Sprintf("%d", r.ContentLength)},
		// {Key: "bytes_sent", Value: r.Header.Get("Content-Length")},
		{Key: "http_user_agent", Value: r.UserAgent()},
		{Key: "remote_addr", Value: r.RemoteAddr},
		{Key: "http_referer", Value: r.Referer()},
		// {Key: "upstream_response_time", Value: "UNKNOWN"},
		{Key: "server_protocol", Value: r.Proto},
		// {Key: "requestuid", Value: "UNKNOWN"},
	}

	var buffer bytes.Buffer
	buffer.WriteString("{")
	for index, pair := range httpLog {
		buffer.WriteString(fmt.Sprintf("%q:%q", pair.Key, pair.Value))
		if index < len(httpLog)-1 {
			buffer.WriteString(",")
		}
	}
	buffer.WriteString("}")
	serviceLogger.LogMessage(buffer.String())
}
