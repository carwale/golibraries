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
	globalConsulAgent 	*objConsulAgent.ConsulAgent
	isLokiLogEnabled 	bool
	serviceLogger 		*gologger.CustomLogger
	globalserviceName			string
)

type LokiLogger struct {
	monitoringKey	string
	consulIP string
	logger *gologger.CustomLogger
	serviceName string
}

func (l *LokiLogger) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	fmt.Println("The logger middleware is executing!")
	next.ServeHTTP(w, r)

	SetBasicConfig(l.monitoringKey, l.consulIP, l.logger, l.serviceName)
}

// SetBasicConfig start point of the request
func SetBasicConfig(key string, consulIP string, logger *gologger.CustomLogger, serviceName string) {
	globalConsulAgent = objConsulAgent.NewConsulAgent(
		objConsulAgent.ConsulHost(consulIP),
		objConsulAgent.Logger(logger),
	)
	serviceLogger = logger
	globalserviceName = serviceName
	go CheckLokiLogStatus(key)
}

// CheckLokiLogStatus continuosly checks if the key has been expired or not
func CheckLokiLogStatus(key string) {
	time.Sleep(10 * time.Second)
	
	// Monitoring key considered here
	bhriguLogger := GetValueFromConsulByKey(key)
	loggerTime, err := time.Parse("01/02/2006 15:04:05", bhriguLogger)
	
	if err != nil {
		isLokiLogEnabled = false
	}

	if loggerTime.Before(time.Now()) {
		isLokiLogEnabled = false
	}

	isLokiLogEnabled = true
}

func LogLokiLogs(r *http.Request, statusCode int) {
	if !isLokiLogEnabled {
		return
	}
	
	lokiLog := []gologger.Pair {
		gologger.Pair{Key: "time_iso8601", Value: time.Now().Format(time.RFC3339)},
		gologger.Pair{Key: "proxyUpstreamName", Value: globalserviceName},
		gologger.Pair{Key: "upstreamStatus", Value: fmt.Sprintf("%d", statusCode)},
		gologger.Pair{Key: "upstream", Value: getIP(r)},
		gologger.Pair{Key: "request_method", Value: r.Method},
		gologger.Pair{Key: "request_uri", Value: GetAbsoluteUrl(r)},
		gologger.Pair{Key: "status", Value: fmt.Sprintf("%d", statusCode)},
		// gologger.Pair{Key: "request_length", Value: fmt.Sprintf("%d", r.ContentLength)},
		// gologger.Pair{Key: "bytes_sent", Value: r.Header.Get("Content-Length")},
		gologger.Pair{Key: "http_user_agent", Value: r.UserAgent()},
		gologger.Pair{Key: "remote_addr", Value: r.RemoteAddr},
		gologger.Pair{Key: "http_referer", Value: r.Referer()},
		// gologger.Pair{Key: "upstream_response_time", Value: "UNKNOWN"},
		gologger.Pair{Key: "server_protocol", Value: r.Proto},
		// gologger.Pair{Key: "requestuid", Value: "UNKNOWN"},
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