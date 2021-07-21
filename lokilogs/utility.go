package lokilogs

import (
	"net"
	"net/http"
)

func getValueFromConsulByKey(key string) string {
	return string(globalConsulAgent.GetValue(key))
}

func getAbsoluteUrl(r *http.Request) string {
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