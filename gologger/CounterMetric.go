package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// CounterMetric : Default histogram message type implementing IMetricVec
type CounterMetric struct {
	counter *prometheus.CounterVec
	logger  *CustomLogger
}

// Update the message with calculated latency
func (msg *CounterMetric) Update(elapsed int64, labels ...string) {
	msg.counter.WithLabelValues(labels...).Inc()
}

//RemoveLogging will stop logging for specific labels
func (msg *CounterMetric) RemoveLogging(labels ...string) {
	ok := msg.counter.DeleteLabelValues(labels...)
	if !ok {
		msg.logger.LogErrorWithoutErrorf("Could not delete metric with labels ", labels)
	}
}

//NewCounterMetric creates a new histrogram message and registers it to prometheus
func NewCounterMetric(hist *prometheus.CounterVec, logger *CustomLogger) *CounterMetric {
	msg := &CounterMetric{hist, logger}
	prometheus.MustRegister(hist)
	return msg
}
