package zipkinmanager

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/carwale/golibraries/gologger"

	"github.com/carwale/golibraries/rabbitmq/channelprovider"
	"github.com/openzipkin/zipkin-go-opentracing"
	"github.com/openzipkin/zipkin-go-opentracing/thrift/gen-go/zipkincore"
	"github.com/streadway/amqp"
)

const defaultQueue = "zipkin"
const defaultQueueBatchInterval = 1

const defaultQueueBatchSize = 100

const defaultQueueMaxBacklog = 1000

// RabbitMQCollector implements Collector by publishing spans to a rabbitmq
// broker.
type RabbitMQCollector struct {
	rabbitMQServers   []string
	ch                *amqp.Channel
	errorChannel      chan *amqp.Error
	queueName         string
	logger            *gologger.CustomLogger
	batchInterval     time.Duration
	batchSize         int
	maxBacklog        int
	batch             []*zipkincore.Span
	spanc             chan *zipkincore.Span
	quit              chan struct{}
	shutdown          chan error
	sendMutex         *sync.Mutex
	batchMutex        *sync.Mutex
	reqCallback       RequestCallback
	rabbitmqConnected bool
}

// RequestCallback receives the initialized request from the Collector before
// sending it over the wire. This allows one to plug in additional headers or
// do other customization.
type RequestCallback func(*http.Request)

// RabbitmqOption sets a parameter for the rabbitmqCollector
type RabbitmqOption func(c *RabbitMQCollector)

// RabbitmqQueueName sets the queue name on which zipkin will send messages.
//Defaults to "zipkin"
func RabbitmqQueueName(t string) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.queueName = t }
}

// RabbitmqBatchSize sets the maximum batch size, after which a collect will be
// triggered. The default batch size is 100 traces.
func RabbitmqBatchSize(n int) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.batchSize = n }
}

// RabbitmqMaxBacklog sets the maximum backlog size,
// when batch size reaches this threshold, spans from the
// beginning of the batch will be disposed
func RabbitmqMaxBacklog(n int) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.maxBacklog = n }
}

// RabbitmqBatchInterval sets the maximum duration we will buffer traces before
// emitting them to the collector. The default batch interval is 1 second.
func RabbitmqBatchInterval(d time.Duration) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.batchInterval = d }
}

// RabbitmqRequestCallback registers a callback function to adjust the collector
// *http.Request before it sends the request to Zipkin.
func RabbitmqRequestCallback(rc RequestCallback) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.reqCallback = rc }
}

//RabbitMQLogger sets the logger for consul
//Defaults to consul logger
func RabbitMQLogger(customLogger *gologger.CustomLogger) RabbitmqOption {
	return func(c *RabbitMQCollector) { c.logger = customLogger }
}

// NewRabbitMQCollector returns a new rabbitmq-backed Collector. addrs should be a
// slice of TCP endpoints of the form "host:port".
func NewRabbitMQCollector(servers []string, options ...RabbitmqOption) (zipkintracer.Collector, error) {

	c := &RabbitMQCollector{
		queueName:         defaultQueue,
		errorChannel:      make(chan *amqp.Error),
		batchInterval:     defaultQueueBatchInterval * time.Second,
		batchSize:         defaultQueueBatchSize,
		maxBacklog:        defaultQueueMaxBacklog,
		batch:             []*zipkincore.Span{},
		spanc:             make(chan *zipkincore.Span),
		quit:              make(chan struct{}, 1),
		shutdown:          make(chan error, 1),
		sendMutex:         &sync.Mutex{},
		batchMutex:        &sync.Mutex{},
		rabbitMQServers:   servers,
		rabbitmqConnected: true,
	}

	for _, option := range options {
		option(c)
	}

	if c.logger == nil {
		c.logger = gologger.NewLogger()
	}
	c.logger.LogDebug(servers[0])
	timeout := time.After(5 * time.Second)
	flag := make(chan bool, 0)
	go func() {
		chPro := channelprovider.NewChannelProviderWithServers(c.logger, c.rabbitMQServers)
		channel, err := chPro.GetChannel()
		if err != nil {
			c.logger.LogError("Error getting channel for zipkin", err)
			flag <- false
		} else {
			c.ch = channel
			c.ch.NotifyClose(c.errorChannel)
			flag <- true
		}
	}()
	select {

	case f := <-flag:
		if !f {
			c.rabbitmqConnected = false
			return nil, errors.New("Error getting channel for zipkin")
		}
	case <-timeout:
		{
			c.rabbitmqConnected = false
			return nil, errors.New("Error getting channel for zipkin. Timeout 5 secs")
		}
	}

	go c.loop()

	return c, nil
}

// Collect implements Collector.
func (c *RabbitMQCollector) Collect(s *zipkincore.Span) error {
	c.spanc <- s
	return nil
}

//Close implements Collector.
func (c *RabbitMQCollector) Close() error {
	close(c.quit)
	return <-c.shutdown
}

func (c *RabbitMQCollector) loop() {
	var (
		nextSend = time.Now().Add(c.batchInterval)
		ticker   = time.NewTicker(c.batchInterval / 10)
		tickc    = ticker.C
	)
	defer ticker.Stop()

	for {
		select {
		case span := <-c.spanc:
			currentBatchSize := c.append(span)
			if currentBatchSize >= c.batchSize {
				nextSend = time.Now().Add(c.batchInterval)
				go c.send()
			}
		case <-tickc:
			if time.Now().After(nextSend) {
				nextSend = time.Now().Add(c.batchInterval)
				go c.send()
			}
		case <-c.errorChannel:
			c.logger.LogErrorWithoutError("Error in rabbitmq channel for zipkin. Trying to reconnect")
			c.rabbitmqConnected = false
			c.errorChannel = nil
			go func() {
				chPro := channelprovider.NewChannelProviderWithServers(c.logger, c.rabbitMQServers)
				c.ch, _ = chPro.GetChannel()
				c.errorChannel = make(chan *amqp.Error)
				c.ch.NotifyClose(c.errorChannel)
				c.rabbitmqConnected = true
				c.logger.LogErrorWithoutError("Reconnected to rabbitmq")
			}()
		case <-c.quit:
			c.shutdown <- c.send()
			return
		}
	}
}

func (c *RabbitMQCollector) append(span *zipkincore.Span) (newBatchSize int) {
	c.batchMutex.Lock()
	defer c.batchMutex.Unlock()

	c.batch = append(c.batch, span)
	if len(c.batch) > c.maxBacklog {
		dispose := len(c.batch) - c.maxBacklog
		c.logger.LogErrorWithoutError("backlog too long, disposing spans. Total disposed messages " + strconv.Itoa(dispose))
		c.batch = c.batch[dispose:]
	}
	newBatchSize = len(c.batch)
	return
}

func (c *RabbitMQCollector) send() error {

	if !c.rabbitmqConnected {
		return nil
	}

	// in order to prevent sending the same batch twice
	c.sendMutex.Lock()
	defer c.sendMutex.Unlock()

	// Select all current spans in the batch to be sent
	c.batchMutex.Lock()
	sendBatch := c.batch[:]
	c.batchMutex.Unlock()

	// Do not send an empty batch
	if len(sendBatch) == 0 {
		return nil
	}
	bb := httpSerialize(sendBatch)

	err := c.ch.Publish("", c.queueName, false, false, amqp.Publishing{
		Body:        bb.Bytes(),
		ContentType: "application/json",
	})
	if err != nil {
		c.logger.LogError("Error in publishing rabbitmq message for zipkin", err)
	}

	// Remove sent spans from the batch
	c.batchMutex.Lock()
	c.batch = c.batch[len(sendBatch):]
	c.batchMutex.Unlock()

	return err
}

func httpSerialize(spans []*zipkincore.Span) *bytes.Buffer {
	t := thrift.NewTMemoryBuffer()
	p := thrift.NewTBinaryProtocolTransport(t)
	if err := p.WriteListBegin(thrift.STRUCT, len(spans)); err != nil {
		panic(err)
	}
	for _, s := range spans {
		if err := s.Write(p); err != nil {
			panic(err)
		}
	}
	if err := p.WriteListEnd(); err != nil {
		panic(err)
	}
	return t.Buffer
}
