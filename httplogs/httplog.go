package httplogs

import (
	"bytes"
	"fmt"
	"net/http"

	"strconv"
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

// HTTPAccessLoggingWrapper is wrapper to neable access logs
func HTTPAccessLoggingWrapper(h http.Handler) http.Handler {
	// fmt.Println("httplogs.HTTPAccessLoggingWrapper called")
	loggingFn := func(w http.ResponseWriter, r *http.Request) {
		lrw := httploggingResponseWriter{
			ResponseWriter: w,
			rData: &responseData{
				status: 0,
				size:   0,
			},
		}

		h.ServeHTTP(&lrw, r) // inject our implementation of http.ResponseWriter
		logHTTPLogs(r, lrw.rData.status, lrw.rData.size)
	}
	return http.HandlerFunc(loggingFn)
}

// InitLogging acts as a constructor to initialize the logging service and
// initailize the struct
func InitLogging(key string, consulIP string, logger *gologger.CustomLogger, serviceName string) {
	// fmt.Println("httplogs.Constructor called")
	setBasicConfig(key, consulIP, logger, serviceName)
}

// SetBasicConfig start point of the request
func setBasicConfig(key string, consulIP string, logger *gologger.CustomLogger, serviceName string) {
	// fmt.Println("httplogs.setBasicConfig called")
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
		serviceLogger.LogDebug("The value of access log for "+ globalserviceName +" is:" + strconv.FormatBool(isMonitoringLogEnabled))
		time.Sleep(10 * time.Second)

		// Monitoring key considered here
		monitoringLoggerTime := getValueFromConsulByKey(key)
		if monitoringLoggerTime == "" {
			isMonitoringLogEnabled = false
			continue
		}

		loggerTime, err := time.Parse("01/02/2006 15:04:05", monitoringLoggerTime)
		if err != nil {
			isMonitoringLogEnabled = false
			continue
		}

		if loggerTime.Before(time.Now()) {
			isMonitoringLogEnabled = false
			continue
		}

		isMonitoringLogEnabled = true
	}
}

func logHTTPLogs(r *http.Request, statusCode int, size int) {
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
		{Key: "request_length", Value: fmt.Sprintf("%d", size)},
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
		if index == 0 {
			buffer.WriteString(fmt.Sprintf("%q:%q", pair.Key, pair.Value))
		} else {
			buffer.WriteString(fmt.Sprintf(",%q:%q", pair.Key, pair.Value))
		}
	}
	buffer.WriteString("}")
	serviceLogger.LogMessage(buffer.String())
}
