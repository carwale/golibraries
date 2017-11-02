package rabbitmq

import (
	"log"
	"github.com/streadway/amqp"
	"strings"
	"encoding/json"
	"math/rand"
	"time"
)

var (
	rabbitMqPortInfo string = ":5672/"
	exchangeSuffix string = "-Exchange"
	keySuffix string = "-Key"
	dlQueueSuffix string = "-DL"
	ttl int32 = 30000
	MaxDelay int = 3600	// Max delay of 1hr
)

type IProcessor interface{
	ProcessMessage( map[string]interface{} ) bool
}

func logOnError(err error, msg string) {
	if err != nil {
		log.Printf("%s: %s\n", msg, err)
	}
}

// Try to connect to the RabbitMQ server as
// long as it takes to establish a connection
func connectToRabbitMQ(ch **amqp.Channel,rabbitMqServers []string,queueName string,args amqp.Table,notify chan bool,errorchannel chan *amqp.Error) {

	connectDelay := 1
	for {
		rand.Seed(time.Now().UTC().UnixNano())
		uri := "amqp://guest:guest@" + rabbitMqServers[rand.Intn(len(rabbitMqServers))] + rabbitMqPortInfo
		conn, err := amqp.Dial(uri)

		if err == nil {
			log.Printf(" Connected to %s\n", uri)
			log.Printf(" Creating a channel to RabbitMQ ")
			*ch, err = conn.Channel()

			queueName = strings.ToUpper(queueName)
			exchangeType := "direct"
			exchangeName := queueName + exchangeSuffix
			routingKey := queueName + keySuffix

			if err == nil {
				q, err1 := (*ch).QueueDeclare(
					queueName,  
					true,   // durable
					false,   // delete when usused
					false,   // exclusive
					false,   // no-wait
					args,	// arguments
				)

				logOnError(err, "Failed to declare a queue")

				err2 := (*ch).ExchangeDeclare(
							exchangeName,   // name
							exchangeType, // type
							true,	// durable
							false,  // auto-deleted
							false,  // internal
							false,  // no-wait
							nil,	  // arguments
					)

				logOnError(err, "Failed to declare an exchange")

				err3 := (*ch).QueueBind(
							q.Name, // queue name
							routingKey,  // routing key
							exchangeName, // exchange
							false,
							nil)

				logOnError(err, "Failed to bind a queue")
				if err1 == nil && err2 == nil && err3 == nil{
					notify <- true
					(*ch).NotifyClose(errorchannel)
					return
				}
			}
			logOnError(err, "Failed to create a channel")
		}
		logOnError(err, "Failed to connect to RabbitMQ")
		log.Printf("Trying to reconnect to RabbitMQ at %s\n", uri)

		// Exponential backoff retry with some Max delay
		if (connectDelay < MaxDelay){
			connectDelay *= 2
		} else {
			connectDelay = MaxDelay
		}
		time.Sleep(time.Duration(connectDelay) * time.Second)
	}
}

func InitializeConnWithErrChannel(ch **amqp.Channel, errorchannel chan *amqp.Error, rabbitMqServers []string,queueName string,args amqp.Table) chan bool{
	
	log.Printf("Creating Connection\n")
	notifyChannel := make(chan bool)
	if (*ch) != nil{
		(*ch).Close()
	}
	go connectToRabbitMQ(ch,rabbitMqServers,queueName,args,notifyChannel,errorchannel)
	
	return notifyChannel
}


func InitializeConn(ch **amqp.Channel,rabbitMqServers []string,queueName string,args amqp.Table) chan bool {
	log.Printf("Creating Connection\n")
	notifyChannel := make(chan bool)
	errorchannel := make(chan *amqp.Error,3)
	go func() {
		for {
			err := <-errorchannel
			if(err != nil){
				connectToRabbitMQ(ch,rabbitMqServers,queueName,args,notifyChannel,errorchannel)
			}
		}
	}()

	// establish the rabbitmq connection by sending
	// an error and thus calling the error callback
	errorchannel <- amqp.ErrClosed
	return notifyChannel
}

func FuncConsumer(queueName string, Processor func( map[string]interface{} ) bool, rabbitMqServers []string) {
	queueName = strings.ToUpper(queueName)
	dlQueueName := queueName + dlQueueSuffix
	var ch *amqp.Channel

	// DL Queue args
	args := make(amqp.Table) 
	args["x-ha-policy"] = "all"
	args["x-dead-letter-exchange"] = queueName + exchangeSuffix
	args["x-dead-letter-routing-key"] = queueName + keySuffix
	args["x-message-ttl"] =  ttl

	createdChannel := InitializeConn(&ch,rabbitMqServers,dlQueueName,args)
	for {
		connected := <-createdChannel
		if connected {
			ch.Qos(5,0,false); // Per consumer limit
				
			log.Printf(" Waiting for Messages to process. To exit press CTRL+C ")
			msgs, err := ch.Consume(
				queueName, // queue
				"Consumer",  // consumer
				false,   // auto-ack
				false,  // exclusive
				false,  // no-local
				false,  // no-wait
				nil,	// args
			)
			logOnError(err, "Failed to register a consumer")
			
			for msg := range msgs {

				byt := msg.Body
				
				var data map[string]interface{}
				err := json.Unmarshal(byt, &data) 

				logOnError(err, "Failed to parse the data from json")
				isProcessed := true		// If msg is not in right format then discard it.
				if err == nil{
					isProcessed = Processor(data)
				}
				if isProcessed {
					log.Printf("message successfully processed\n")
					msg.Ack(true)
				} else {
					msg.Nack(true, false)
					_, isExists := data["count"]
					if isExists {
						data["count"] = data["count"].(float64) + 1
					} else {
						data["count"] = 1
					}
					log.Printf("Requeue count  %s" ,data["count"])
					dataBytes, err := json.Marshal(data)
					logOnError(err, "Failed to parse the data in json")
					Publisher(dataBytes,ch,dlQueueName)
				}
			}
		}
	}
}

func IConsumer(queueName string, Processor IProcessor, rabbitMqServers []string) {
	FuncConsumer(queueName,Processor.ProcessMessage,rabbitMqServers)
}

func Publisher(msg []byte, ch *amqp.Channel,queueName string) {
	queueName = strings.ToUpper(queueName)
	exchangeName := queueName + exchangeSuffix
	routingKey := queueName + keySuffix

	 publish(msg, ch, exchangeName, routingKey)
}

func publish(msg []byte, ch *amqp.Channel,exchangeName string,routingKey string) {
	if ch != nil {
		err := ch.Publish(
			exchangeName, // exchange
			routingKey,	   // routing key
			false,		  // mandatory (This flag tells the server how to react if the message cannot be routed to a queue. 
							//If this flag is set to true, the server will return an unroutable message to the producer 
							//with a `basic.return` AMQP method. If this flag is set to false, the server silently drops the message)
			false,		 // immediate
			amqp.Publishing{
				ContentType: "application/octet-stream",
				DeliveryMode:   2,
				Body:	   msg,
			})
		logOnError(err, "Failed to publish a message")
	}
}