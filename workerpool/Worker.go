package workerpool

import(
	"log"
)

// Interface for the Job to be processed
type IJob interface{
	Process() error
}

// Interface for Worker
type IWorker interface{
	Start()
	Stop()
}

// Default Worker implementation
type Worker struct {
	WorkerPool   	chan chan IJob 	// A pool of workers channels that are registered in the dispatcher
	JobChannel   	chan IJob		// Channel through which a job is received by the worker
	Quit         	chan bool		// Channel for quit signal
	WorkerNumber 	int 			// Worker Number
}

func (w *Worker) Start(){
	go func() {
		for {
			w.WorkerPool <- w.JobChannel
			select {
				case job := <-w.JobChannel:	// Worker is waiting here to receive job from JobQueue
					err := job.Process()	// Worker is Processing the job
					if err != nil{
						log.Printf("Some error occurred in processing: %s\n", err)
					}

				case <-w.Quit:
					// Signal to stop the worker
					return
			}
		}
	}()	
}

func (w *Worker) Stop(){
	go func() {
		w.Quit <- true
	}()
}

func NewWorker(workerPool chan chan IJob, number int) IWorker {
	return &Worker{
		WorkerPool:   workerPool,
		JobChannel:   make(chan IJob),
		Quit:         make(chan bool),
		WorkerNumber: number}
}

type Dispatcher struct {
	WorkerPool 	chan chan IJob 	// A pool of workers channels that are registered with the dispatcher
	MaxWorkers 	int
	NewWorker 	func(chan chan IJob, int) IWorker
	JobQueue	chan IJob
}

func (d *Dispatcher) run() {
	// starting n number of workers
	for i := 0; i < d.MaxWorkers; i++ {
		go func(j int){
			worker := d.NewWorker(d.WorkerPool, j)	// Initialise a new worker
			worker.Start()		// Start the worker
		}(i)
	}

	go d.dispatch() 	// Start the dispatcher
}

func (d *Dispatcher) dispatch() {
	for {
		select {
		case job := <- d.JobQueue:
			// a job request has been received
			go func(job IJob) {
				// try to obtain a worker job channel that is available.
				// this will block until a worker is idle
				jobChannel := <-d.WorkerPool
				// dispatch the job to the worker job channel
				jobChannel <- job
			}(job)
		}
	}
}

func newDispatcher(jobQueue chan IJob, newWorker func(chan chan IJob, int) IWorker, maxWorkers int) *Dispatcher {
	pool := make(chan chan IJob, maxWorkers)
	return &Dispatcher{
		WorkerPool: pool,
		MaxWorkers: maxWorkers,
		NewWorker: newWorker,
		JobQueue: jobQueue}
}

// Dispatcher using Default worker
func InitializeDefaultDispatcher(maxWorkers int) *Dispatcher{
	jobQueue := make(chan IJob)
	dispatcher := newDispatcher(jobQueue, NewWorker, maxWorkers)
	dispatcher.run()
	return dispatcher
}

// Dispatcher using custom implementation of the worker
func InitializeDispatcher(newWorker func(chan chan IJob, int) IWorker, maxWorkers int) *Dispatcher{
	jobQueue := make(chan IJob)
	dispatcher := newDispatcher(jobQueue, newWorker, maxWorkers)
	dispatcher.run()
	return dispatcher
}