package gologger

import (
	"time"
)

// IMultiLogger : Interface for Multi message logger
type IMultiLogger interface {
	// To measure time elapsed between any two points in the code,
	// Start the time logger by Tic(MessageDesc) and end the time logger by calling Toc(MessageDesc,time)
	Tic() time.Time
	Toc(time.Time, string, ...string)
	IncVal(int64, string, ...string)
	SubVal(int64, string, ...string)
	SetVal(int64, string, ...string)
	AddNewMetric(string, IMetricVec)
}

// IMetricVec : Interface to implement for Message type
type IMetricVec interface {
	UpdateTime(int64, ...string)
	//Method to count increments or gauges
	AddValue(int64, ...string)
	// Method to subtract from the counter
	SubValue(int64, ...string)
	// Method to set the counter
	SetValue(int64, ...string)
	// Method to Remove the label from the metric
	RemoveLogging(...string)
}
