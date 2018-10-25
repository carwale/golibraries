package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// HistogramMetric : Default histogram message type implementing IMetricVec
type HistogramMetric struct {
	histogram *prometheus.HistogramVec
	logger    *CustomLogger
}

// Update the message with calculated latency
func (msg *HistogramMetric) Update(elapsed int64, labels ...string) {
	msg.histogram.WithLabelValues(labels...).Observe(float64(elapsed) / 1000)
}

//RemoveLogging will stop logging for specific labels
func (msg *HistogramMetric) RemoveLogging(labels ...string) {
	ok := msg.histogram.DeleteLabelValues(labels...)
	if !ok {
		msg.logger.LogErrorWithoutErrorf("Could not delete metric with labels ", labels)
	}
}

//NewHistogramMetric creates a new histrogram message and registers it to prometheus
func NewHistogramMetric(hist *prometheus.HistogramVec, logger *CustomLogger) *HistogramMetric {
	msg := &HistogramMetric{hist, logger}
	prometheus.MustRegister(hist)
	return msg
}
