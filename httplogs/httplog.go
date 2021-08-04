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

var _gLogConfig *GlobalParameters

// GlobalParameters is the class used to store global variables
type GlobalParameters struct {
	consulAgent            *objConsulAgent.ConsulAgent
	serviceLogger          *gologger.CustomLogger
	serviceName            string
	consulIP               string
	isMonitoringLogEnabled bool
}

// Options sets a variable of GlobalParameters
type Options func(lb *GlobalParameters)

// HTTPAccessLoggingWrapper is wrapper to enable access logs
func HTTPAccessLoggingWrapper(h http.Handler) http.Handler {
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
func InitLogging(serviceName string, options ...Options) {
	_gLogConfig = setDefaultConfig(serviceName)
	for _, option := range options {
		option(_gLogConfig)
	}
	if _gLogConfig.serviceLogger == nil {
		SetLogger(gologger.NewLogger())
	}
	setBasicConfig(serviceName)
}

// SetLogger (mandatory) parameter in order to configure logger
func SetLogger(customLogger *gologger.CustomLogger) Options {
	return func(lb *GlobalParameters) { lb.serviceLogger = customLogger }
}

// SetConsulIP (mandatory) default value is localhost, thus service has to change the
// consul ip based on environment
func SetConsulIP(consultIP string) Options {
	return func(al *GlobalParameters) { al.consulIP = consultIP }
}

func setDefaultConfig(serviceName string) *GlobalParameters {
	return &GlobalParameters{
		consulIP:      "127.0.0.1:8500",
		serviceName:   serviceName,
	}
}

// SetBasicConfig start point of the request
func setBasicConfig(serviceName string) {
	_gLogConfig.consulAgent = objConsulAgent.NewConsulAgent(
		objConsulAgent.ConsulHost(_gLogConfig.consulIP),
		objConsulAgent.Logger(_gLogConfig.serviceLogger),
	)

	monitoringKey := getMonitoringKey(serviceName)
	go checkHTTPLogStatus(monitoringKey)
}

// infinite loop checking the key 'access_logs'
func checkHTTPLogStatus(key string) {
	for {
		_gLogConfig.serviceLogger.LogDebug("The value of access log for " + _gLogConfig.serviceName + " is:" + strconv.FormatBool(_gLogConfig.isMonitoringLogEnabled))
		time.Sleep(5 * time.Minute)

		// Monitoring key considered here
		monitoringLoggerTime := getValueFromConsulByKey(key)
		if monitoringLoggerTime == "" {
			_gLogConfig.isMonitoringLogEnabled = false
			continue
		}

		loggerTime, err := time.Parse("01/02/2006 15:04:05", monitoringLoggerTime)
		if err != nil {
			_gLogConfig.isMonitoringLogEnabled = false
			continue
		}

		if loggerTime.Before(time.Now()) {
			_gLogConfig.isMonitoringLogEnabled = false
			continue
		}

		_gLogConfig.isMonitoringLogEnabled = true
	}
}

func logHTTPLogs(r *http.Request, statusCode int, size int) {
	if !_gLogConfig.isMonitoringLogEnabled {
		return
	}

	httpLog := []gologger.Pair{
		{Key: "time_iso8601", Value: time.Now().Format(time.RFC3339)},
		{Key: "proxyUpstreamName", Value: _gLogConfig.serviceName},
		{Key: "upstreamStatus", Value: fmt.Sprintf("%d", statusCode)},
		{Key: "upstream", Value: getIP(r)},
		{Key: "request_method", Value: r.Method},
		{Key: "request_uri", Value: getAbsoluteURL(r)},
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
	_gLogConfig.serviceLogger.LogMessage(buffer.String())
}
