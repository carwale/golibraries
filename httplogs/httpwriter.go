package httplogs

import (
	"net/http"
)

type (
	responseData struct {
		status int
		size   int
	}

	httploggingResponseWriter struct {
		http.ResponseWriter
		rData *responseData
	}
)

func (r *httploggingResponseWriter) Write(b []byte) (int, error) {
	size, err := r.ResponseWriter.Write(b)
	r.rData.size += size
	return size, err
}

func (r *httploggingResponseWriter) WriteHeader(statusCode int) {
	r.ResponseWriter.WriteHeader(statusCode)
	r.rData.status = statusCode
}
