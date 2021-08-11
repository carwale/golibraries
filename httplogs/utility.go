package httplogs

import (
	"net"
	"net/http"
	"github.com/google/uuid"
	"strings"
)

func getValueFromConsulByKey(key string) string {	
	return string(_gLogConfig.consulAgent.GetValue(key))
}

func getAbsoluteURL(r *http.Request) string {
	return r.Host + r.RequestURI
}

func getIP(r *http.Request) string {
	if ipProxy := r.Header.Get("X-Forwarded-For"); len(ipProxy) > 0 {
		return ipProxy
	} else if ipProxy := r.Header.Get("Client-IP"); len(ipProxy) > 0 {
		return ipProxy
	} else if ipProxy := r.Header.Get("X-Original-Forwarded-For"); len(ipProxy) > 0 {
		return ipProxy
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// The key used for log generation should be 'access_logs' for respective service
func getMonitoringKey(serviceName string) string {
	return "Monitoring/" + serviceName + "/access_logs"
}

func getTraceRootID(trace string) string {
	if trace == "" {
		return uuid.New().String()
	}

	var strList = strings.Split(trace, ";")
	for _,str := range strList {
		if strings.HasPrefix(str, "Root") {
			return strings.Split(str, "=")[1];
		}
	}

	return uuid.New().String()
}