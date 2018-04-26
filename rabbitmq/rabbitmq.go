package rabbitmq

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/rabbitmq/channelprovider"
	"github.com/streadway/amqp"
)

const (
	exchangeSuffix       = "-Exchange"
	keySuffix            = "-Key"
	dlQueueSuffix        = "-DL"
	ttl            int32 = 30000 // in milliseconds
)

// IProcessor : interface for consuming messages from queue
type IProcessor interface {
	ProcessMessage(map[string]interface{}) bool
}

// OperationManager manages rabbitmq connections and operations like publish & consume
type OperationManager struct {
	logger          *gologger.CustomLogger
	rabbitMqServers []string
	channelProvider *channelprovider.ChannelProvider
	queueProps      queueProperties
	dlQueueProps    queueProperties
}

// queueProperties struct holds queue details
type queueProperties struct {
	queueName    string
	exchangeType string
	exchangeName string
	routingKey   string
	args         amqp.Table
}

// NewRabbitMQManager : returns RabbitMQ OperationManager.
// panics if empty server list given.
func NewRabbitMQManager(logger *gologger.CustomLogger, rabbitMqServers []string, queueName string) *OperationManager {
	if len(rabbitMqServers) == 0 {
		panic("No rabbitmq servers provided.")
	}
	om := &OperationManager{
		logger:          logger,
		rabbitMqServers: rabbitMqServers,
	}
	om.channelProvider = channelprovider.NewChannelProviderWithServers(om.logger, om.rabbitMqServers)
	// Init queue properties
	queueName = strings.ToUpper(queueName)
	dlQueueName := strings.ToUpper(queueName) + dlQueueSuffix
	om.queueProps = queueProperties{
		queueName:    strings.ToUpper(queueName),
		exchangeType: "direct",
		exchangeName: queueName + exchangeSuffix,
		routingKey:   queueName + keySuffix,
		args:         nil,
	}
	// DL Queue args
	dlargs := make(amqp.Table)
	dlargs["x-ha-policy"] = "all"
	dlargs["x-dead-letter-exchange"] = om.queueProps.exchangeName
	dlargs["x-dead-letter-routing-key"] = om.queueProps.routingKey
	dlargs["x-message-ttl"] = ttl

	om.dlQueueProps = queueProperties{
		queueName:    dlQueueName,
		exchangeType: "direct",
		exchangeName: dlQueueName + exchangeSuffix,
		routingKey:   dlQueueName + keySuffix,
		args:         dlargs,
	}

	return om
}

// NewRabbitmqChannel : initializes the rabbitmq channel.
// Input parameter is a flag to notify error on channel
// NOTE: Add a listener to returned error channel to handle connection errors.
func (om *OperationManager) NewRabbitmqChannel(notifyError bool) (*amqp.Channel, chan *amqp.Error) {
	// Try to connect to the RabbitMQ server as
	// long as it takes to establish a connection
	for {
		ch, err := om.channelProvider.GetChannel()

		if err == nil {
			if notifyError {
				errorchannel := make(chan *amqp.Error, 3)
				ch.NotifyClose(errorchannel)
				return ch, errorchannel
			}
			return ch, nil
		}
	}
}

// SetBindings : declare the queue, exchange and sets bindings between queue and exhange.
// pass `isDL` true to set dead letter bindings for given queuename
func (om *OperationManager) SetBindings(ch *amqp.Channel, isDL bool) error {
	queueName := om.queueProps.queueName
	exchangeName := om.queueProps.exchangeName
	routingKey := om.queueProps.routingKey
	exchangeType := om.queueProps.exchangeType
	args := om.queueProps.args
	if isDL {
		queueName = om.dlQueueProps.queueName
		exchangeName = om.dlQueueProps.exchangeName
		routingKey = om.dlQueueProps.routingKey
		exchangeType = om.dlQueueProps.exchangeType
		args = om.dlQueueProps.args
	}
	_, err := ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // delete when usused
		false, // exclusive
		false, // no-wait
		args,  // arguments
	)
	if err != nil {
		return err
	}

	err = ch.ExchangeDeclare(
		exchangeName, // name
		exchangeType, // type
		true,         // durable
		false,        // auto-deleted
		false,        // internal
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		return err
	}

	err = ch.QueueBind(
		queueName,    // queue name
		routingKey,   // routing key
		exchangeName, // exchange
		false,        // no-wait
		nil,          // arguments
	)
	return err
}

// StartConsumer : starts the consumer from given queue
// Also it declares a dead letter queue and publishes the failed messages to DL
func (om *OperationManager) StartConsumer(processor IProcessor) {
	once := sync.Once{}
	for {
		ch, errChan := om.NewRabbitmqChannel(true)

		ch.Qos(5, 0, false) // Per consumer limit

		om.logger.LogInfo("Waiting for Messages to process")
		deliveryChan, err := ch.Consume(
			om.queueProps.queueName, // queue
			"Consumer",              // consumer
			false,                   // auto-ack
			false,                   // exclusive
			false,                   // no-local
			false,                   // no-wait
			nil,                     // args
		)
		if err != nil {
			om.logger.LogError("Failed to register a consumer", err)
			continue
		}
	consumeLoop:
		for {
			select {
			case err := <-errChan:
				if err != nil {
					om.logger.LogError("Error received on RabbitMQ error channel", err)
					break consumeLoop
				}
			case msg := <-deliveryChan:
				var data map[string]interface{}
				err := json.Unmarshal(msg.Body, &data)
				// If msg is not in right format then discard it
				if err != nil {
					om.logger.LogErrorMessage("Failed to parse the data from json message", err, gologger.Pair{Key: "message_body", Value: string(msg.Body)})
					continue
				}

				// Processing the received message
				isProcessed := processor.ProcessMessage(data)
				if isProcessed {
					om.logger.LogInfo("Message successfully processed")
					msg.Ack(false)
				} else {
					once.Do(func() {
						dlch, _ := om.NewRabbitmqChannel(false)
						// declaring bindings for dead letter queue
						err := om.SetBindings(ch, true)
						if err != nil {
							om.logger.LogError("Failed to set DL queue bindings", err)
						}
						dlch.Close()
					})
					msg.Nack(false, false)

					if _, isExists := data["count"]; isExists {
						data["count"] = data["count"].(int) + 1
					} else {
						data["count"] = 1
					}
					if cnt, _ := data["count"]; cnt.(int) <= 5 {
						dataBytes, err := json.Marshal(data)
						if err != nil {
							om.logger.LogError("Failed to marshal the data to json", err)
							continue
						}
						dlch, _ := om.NewRabbitmqChannel(false)
						om.PublishDL(dlch, dataBytes)
						dlch.Close()
					}
				}

			}
		}
	}
}

// PublishDL : publishes the message bytes to dead letter queue
func (om *OperationManager) PublishDL(ch *amqp.Channel, msg []byte) {
	om.publish(msg, ch, om.dlQueueProps.exchangeName, om.dlQueueProps.routingKey)
}

// Publish : publishes the message bytes to given queue
func (om *OperationManager) Publish(ch *amqp.Channel, msg []byte) {
	om.publish(msg, ch, om.queueProps.exchangeName, om.queueProps.routingKey)
}

func (om *OperationManager) publish(msg []byte, ch *amqp.Channel, exchangeName string, routingKey string) {
	if ch != nil {
		if err := ch.Publish(
			exchangeName, // exchange
			routingKey,   // routing key
			false,        // mandatory (This flag tells the server how to react if the message cannot be routed to a queue.
			//If this flag is set to true, the server will return an unroutable message to the producer
			//with a `basic.return` AMQP method. If this flag is set to false, the server silently drops the message)
			false, // immediate
			amqp.Publishing{
				ContentType:  "application/octet-stream",
				DeliveryMode: 2,
				Body:         msg,
			}); err != nil {
			om.logger.LogError("Failed to publish a message", err)
		}
	} else {
		om.logger.LogErrorWithoutError("RabbitMQ channel is nil")
	}
}
