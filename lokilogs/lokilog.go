package lokilogs

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	objConsulAgent "github.com/carwale/golibraries/consulagent"
	"github.com/carwale/golibraries/gologger"
)

var (
	globalConsulAgent *objConsulAgent.ConsulAgent
	isLokiLogEnabled  bool
	serviceLogger     *gologger.CustomLogger
	globalserviceName string
)

// SetBasicConfig start point of the request
func SetBasicConfig(key string, consulIP string, logger *gologger.CustomLogger, serviceName string) {
	globalConsulAgent = objConsulAgent.NewConsulAgent(
		objConsulAgent.ConsulHost(consulIP),
		objConsulAgent.Logger(logger),
	)
	serviceLogger = logger
	globalserviceName = serviceName

	go checkLokiLogStatus(key)
}

func checkLokiLogStatus(key string) {
	for {
		time.Sleep(10 * time.Second)

		// Monitoring key considered here
		bhriguLogger := getValueFromConsulByKey(key)
		loggerTime, err := time.Parse("01/02/2006 15:04:05", bhriguLogger)

		if err != nil {
			isLokiLogEnabled = false
		}

		if loggerTime.Before(time.Now()) {
			isLokiLogEnabled = false
		}

		isLokiLogEnabled = true
	}
}

// LogLokiLogs display the log based on isLokiLogEnabled flag
func LogLokiLogs(r *http.Request, statusCode int) {
	if !isLokiLogEnabled {
		return
	}

	lokiLog := []gologger.Pair{
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
	for index, pair := range lokiLog {
		buffer.WriteString(fmt.Sprintf("%q:%q", pair.Key, pair.Value))
		if index < len(lokiLog)-1 {
			buffer.WriteString(",")
		}
	}
	buffer.WriteString("}")
	serviceLogger.LogMessage(buffer.String())
}
