package workerpool

import (
	"strconv"
	"time"

	"github.com/carwale/golibraries/gologger"
)

// IJob : Interface for the Job to be processed
type IJob interface {
	Process() error
}

// IWorker : Interface for Worker
type IWorker interface {
	Start()
	Stop()
}

// Worker : Default Worker implementation
type Worker struct {
	WorkerPool   chan chan IJob // A pool of workers channels that are registered in the dispatcher
	JobChannel   chan IJob      // Channel through which a job is received by the worker
	Quit         chan bool      // Channel for Quit signal
	WorkerNumber int            // Worker Number
}

// Start : Start the worker and add to worker pool
func (w *Worker) Start() {
	go func() {
		for {
			w.WorkerPool <- w.JobChannel
			select {
			case job := <-w.JobChannel: // Worker is waiting here to receive job from JobQueue
				job.Process() // Worker is Processing the job

			case <-w.Quit:
				// Signal to stop the worker
				return
			}
		}
	}()
}

// Stop : Calling this method stops the worker
func (w *Worker) Stop() {
	go func() {
		w.Quit <- true
	}()
}

func newWorker(workerPool chan chan IJob, number int) IWorker {
	return &Worker{
		WorkerPool:   workerPool,
		JobChannel:   make(chan IJob),
		Quit:         make(chan bool),
		WorkerNumber: number,
	}
}

// Option sets a parameter for the Dispatcher
type Option func(d *Dispatcher)

// SetMaxWorkers sets the number of workers. Default is 10
func SetMaxWorkers(maxWorkers int) Option {
	return func(d *Dispatcher) {
		if maxWorkers > 0 {
			d.maxWorkers = maxWorkers
		}
	}
}

// SetNewWorker sets the Worker initialisation function in dispatcher
func SetNewWorker(newWorker func(chan chan IJob, int) IWorker) Option {
	return func(d *Dispatcher) {
		d.newWorker = newWorker
	}
}

// SetLogger sets the logger in dispatcher
func SetLogger(logger *gologger.CustomLogger) Option {
	return func(d *Dispatcher) {
		d.logger = logger
	}
}

// Dispatcher holds worker pool, job queue and manages workers and job
// To submit a job to worker pool, use code
// `dispatcher.JobQueue <- job`
type Dispatcher struct {
	workerPool     chan chan IJob // A pool of workers channels that are registered with the dispatcher
	maxWorkers     int
	newWorker      func(chan chan IJob, int) IWorker
	JobQueue       chan IJob
	workerTracker  chan int
	maxUsedWorkers int
	logger         *gologger.CustomLogger
}

func (d *Dispatcher) run() {
	// starting n number of workers
	for i := 0; i < d.maxWorkers; i++ {
		go func(j int) {
			worker := d.newWorker(d.workerPool, j) // Initialise a new worker
			worker.Start()                         // Start the worker
		}(i)
	}
	d.trackWorkers() // Start tracking used workers
	go d.dispatch()  // Start the dispatcher
}

func (d *Dispatcher) dispatch() {
	for {
		select {
		case job := <-d.JobQueue:
			// a job request has been received
			go func(job IJob) {
				// try to obtain a worker job channel that is available.
				// this will block until a worker is idle
				jobChannel := <-d.workerPool
				// track number of workers processing concurrently
				d.workerTracker <- d.maxWorkers - len(d.workerPool)
				// dispatch the job to the worker job channel
				jobChannel <- job
			}(job)
		}
	}
}

func (d *Dispatcher) trackWorkers() {
	ticker := time.NewTicker(time.Duration(60) * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				// push to logger
				if d.maxUsedWorkers > 0 {
					d.logger.LogInfoMessage("max used workers", gologger.Pair{Key: "maxWorkers", Value: strconv.Itoa(d.maxUsedWorkers)})
					d.maxUsedWorkers = 0
				}
			case numWorkers := <-d.workerTracker:
				// update used workers
				if numWorkers > d.maxUsedWorkers {
					d.maxUsedWorkers = numWorkers
				}
			}
		}
	}()
}

// NewDispatcher : returns a new dispatcher. When no options are given, it returns a dispatcher with default settings
// 10 Workers and `newWorker` initialisation and default logger which logs to graylog @ 127.0.0.1:11100.
// This is not in use. So it is prety much useless.
// Set log level to INFO to track max used workers.
func NewDispatcher(options ...Option) *Dispatcher {
	d := &Dispatcher{
		maxWorkers:    10,
		newWorker:     newWorker,
		JobQueue:      make(chan IJob),
		workerTracker: make(chan int, 10),
		logger:        gologger.NewLogger(gologger.SetLogLevel("INFO")),
	}

	for _, option := range options {
		option(d)
	}

	d.workerPool = make(chan chan IJob, d.maxWorkers)
	d.run()
	return d
}

// InitializeDefaultDispatcher : Dispatcher using Default worker
// This method will be deprecated. Use NewDispatcher(options) instead
func InitializeDefaultDispatcher(maxWorkers int) *Dispatcher {
	return NewDispatcher(SetMaxWorkers(maxWorkers))
}

// InitializeDispatcher : Dispatcher using custom implementation of the worker
// This method will be deprecated. Use NewDispatcher(options) instead
func InitializeDispatcher(customWorker func(chan chan IJob, int) IWorker, maxWorkers int) *Dispatcher {
	return NewDispatcher(SetMaxWorkers(maxWorkers), SetNewWorker(customWorker))
}
