package gologger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// CounterMetric : Default histogram message type implementing IMetricVec
type CounterMetric struct {
	counter *prometheus.CounterVec
	logger  *CustomLogger
}

// Update is a do nothing operation for counter metric
func (msg *CounterMetric) Update(elapsed int64, labels ...string) {
	msg.logger.LogWarning("Cannot use Update for counter metric")
}

// Count will increment the counter by value
func (msg *CounterMetric) Count(count int64, labels ...string) {
	msg.counter.WithLabelValues(labels...).Add(float64(count))
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
