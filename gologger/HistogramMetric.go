package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// HistogramMetric : Default histogram message type implementing IMetricVec
type HistogramMetric struct {
	histogram *prometheus.HistogramVec
	logger    *CustomLogger
}

// UpdateTime the message with calculated latency
func (msg *HistogramMetric) UpdateTime(elapsed int64, labels ...string) {
	msg.histogram.WithLabelValues(labels...).Observe(float64(elapsed) / 1000)
}

// AddValue is a do nothing function for histogram
func (msg *HistogramMetric) AddValue(count int64, labels ...string) {
	msg.logger.LogWarning("Cannot use IncValue for histogram metric")
}

// SubValue is a do nothing function for histogram
func (msg *HistogramMetric) SubValue(count int64, labels ...string) {
	msg.logger.LogWarning("Cannot use SubValue for histogram metric")
}

// SetValue is a do nothing function for histogram
func (msg *HistogramMetric) SetValue(count int64, labels ...string) {
	msg.logger.LogWarning("Cannot use SetValue for histogram metric")
}

// RemoveLogging will stop logging for specific labels
func (msg *HistogramMetric) RemoveLogging(labels ...string) {
	ok := msg.histogram.DeleteLabelValues(labels...)
	if !ok {
		msg.logger.LogErrorWithoutErrorf("Could not delete metric with labels %v", labels)
	}
}

// NewHistogramMetric creates a new histrogram message and registers it to prometheus
func NewHistogramMetric(hist *prometheus.HistogramVec, logger *CustomLogger) *HistogramMetric {
	msg := &HistogramMetric{hist, logger}
	prometheus.MustRegister(hist)
	return msg
}
