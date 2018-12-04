package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// GaugeMetric : Default Gauge message type implementing IMetricVec
type GaugeMetric struct {
	gauge  *prometheus.GaugeVec
	logger *CustomLogger
}

// UpdateTime is a do nothing operation for counter metric
func (msg *GaugeMetric) UpdateTime(elapsed int64, labels ...string) {
	msg.logger.LogWarning("Cannot use Update for counter metric")
}

// AddValue will increment the counter by value
func (msg *GaugeMetric) AddValue(count int64, labels ...string) {
	msg.gauge.WithLabelValues(labels...).Add(float64(count))
}

// SubValue will decrement the counter by value
func (msg *GaugeMetric) SubValue(count int64, labels ...string) {
	msg.gauge.WithLabelValues(labels...).Sub(float64(count))
}

// SetValue will set the counter to that value
func (msg *GaugeMetric) SetValue(count int64, labels ...string) {
	msg.gauge.WithLabelValues(labels...).Set(float64(count))
}

//RemoveLogging will stop logging for specific labels
func (msg *GaugeMetric) RemoveLogging(labels ...string) {
	ok := msg.gauge.DeleteLabelValues(labels...)
	if !ok {
		msg.logger.LogErrorWithoutErrorf("Could not delete metric with labels ", labels)
	}
}

//NewGaugeMetric creates a new gauge message and registers it to prometheus
func NewGaugeMetric(counter *prometheus.GaugeVec, logger *CustomLogger) *GaugeMetric {
	msg := &GaugeMetric{counter, logger}
	prometheus.MustRegister(counter)
	return msg
}
