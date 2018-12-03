package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// CounterMetric : Default histogram message type implementing IMetricVec
type CounterMetric struct {
	counter *prometheus.CounterVec
	logger  *CustomLogger
}

// UpdateTime is a do nothing operation for counter metric
func (msg *CounterMetric) UpdateTime(elapsed int64, labels ...string) {
	msg.logger.LogWarning("Cannot use Update for counter metric")
}

// AddValue will increment the counter by value
func (msg *CounterMetric) AddValue(count int64, labels ...string) {
	msg.counter.WithLabelValues(labels...).Add(float64(count))
}

// SubValue will not do anything. It is not allowed in counters
func (msg *CounterMetric) SubValue(count int64, labels ...string) {
	msg.logger.LogWarning("Cannot subtract values from counters")
}

// SetValue will not do anything. It is not allowed in counters
func (msg *CounterMetric) SetValue(count int64, labels ...string) {
	msg.logger.LogWarning("Cannot reset counters")
}

//RemoveLogging will stop logging for specific labels
func (msg *CounterMetric) RemoveLogging(labels ...string) {
	ok := msg.counter.DeleteLabelValues(labels...)
	if !ok {
		msg.logger.LogErrorWithoutErrorf("Could not delete metric with labels ", labels)
	}
}

//NewCounterMetric creates a new histrogram message and registers it to prometheus
func NewCounterMetric(counter *prometheus.CounterVec, logger *CustomLogger) *CounterMetric {
	msg := &CounterMetric{counter, logger}
	prometheus.MustRegister(counter)
	return msg
}
