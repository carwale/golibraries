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
	// Starts the logger in a go routine
	Run()
	AddNewMetric(string, IMetricVec)
}

// IMetricVec : Interface to implement for Message type
type IMetricVec interface {
	Update(int64, ...string)
	//Method to count increments or gauges
	Count(int64, ...string)
	// Method to Remove the label from the metric
	RemoveLogging(...string)
}
