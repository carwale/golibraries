package kafka

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/carwale/golibraries/gologger"
	"github.com/confluentinc/confluent-kafka-go/kafka"
)

// Producer carries all the settings for the kafka producer
type Producer struct {
	logger                *gologger.CustomLogger
	config                *kafka.ConfigMap
	BrokerServers         string
	IsAutoEventLogEnabled bool
	eventBlockChannel     chan bool
	producer              *kafka.Producer
	EventsChannel         chan kafka.Event
	publishChannel        chan *kafka.Message
	CloseChannel          chan os.Signal
}

func (kp *Producer) startEventLogging() {
	go func() {
		for {
			select {
			case event := <-kp.EventsChannel:
				if !kp.IsAutoEventLogEnabled {
					continue
				}
				switch eventType := event.(type) {
				case *kafka.Message:
					m := eventType
					if m.TopicPartition.Error != nil {
						kp.logger.LogError(fmt.Sprintf("Error received on error channel %v", m.TopicPartition), m.TopicPartition.Error)
					} else {
						kp.logger.LogDebugf("Delivered message to topic %s [%d] at offset %v",
							*m.TopicPartition.Topic, m.TopicPartition.Partition, m.TopicPartition.Offset)
					}
				case kafka.Error:
					// Errors should generally be considered
					// informational, the client will try to
					// automatically recover.
					kp.logger.LogError(fmt.Sprintf("Error: %v\n", eventType.Code()), eventType)
				}
			}
		}
	}()
}

func (kp *Producer) setGracefulCleaning() {
	go func() {
		_ = <-kp.CloseChannel
		kp.logger.LogWarning("Caught closing signal in producer : terminating")
		kp.producer.Flush(30000)
		kp.producer.Close()
		kp.logger.LogWarning("Gracefully closed producer")
	}()
}

//PublishMessageToTopic publishes message to topic
func (kp *Producer) PublishMessageToTopic(msg *[]byte, topic string) {
	kp.publishChannel <- &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Value: *msg,
	}
}

//PublishMessageToTopicWithKey publishes message to topic with key
func (kp *Producer) PublishMessageToTopicWithKey(msg *[]byte, topic string, key string) {
	kp.publishChannel <- &kafka.Message{TopicPartition: kafka.TopicPartition{
		Topic:     &topic,
		Partition: kafka.PartitionAny,
	},
		Key:   []byte(key),
		Value: *msg,
	}
}

//CreateTopic creats a new topic if it does not exists
func (kp *Producer) CreateTopic(topicName string) error {
	adminClient, err := kafka.NewAdminClientFromProducer(kp.producer)
	if err != nil {
		kp.logger.LogError("Could not create kafka admin client", err)
	}
	defer adminClient.Close()
	metadata, err := adminClient.GetMetadata(nil, true, 1000)
	if err != nil {
		kp.logger.LogError("Could not get metadata from kafka admin client", err)
		return err
	}
	_, ok := metadata.Topics[topicName]
	if !ok {
		topic, err := adminClient.CreateTopics(context.Background(), []kafka.TopicSpecification{
			{Topic: topicName, NumPartitions: 1, ReplicationFactor: 2},
		})
		if err != nil {
			kp.logger.LogError("Could not create kafka admin client", err)
			return err
		}
		kp.logger.LogWarning("created topic: " + topic[0].Topic)
	}
	return nil
}

// ProducerOption sets a parameter for the KafkaProducer
type ProducerOption func(l *Producer)

// SetProducerCustomConfig sets the custom config for kafka
func SetProducerCustomConfig(customConfig map[string]interface{}) ProducerOption {
	return func(kp *Producer) {
		if customConfig != nil {
			for k, v := range customConfig {
				kp.config.SetKey(k, v)
			}
		}
	}
}

//ProducerLogger sets the logger for consul
//Defaults to consul logger
func ProducerLogger(customLogger *gologger.CustomLogger) ProducerOption {
	return func(kp *Producer) { kp.logger = customLogger }
}

//EnableEventLogging will enable event logging. By default it is disabled
func EnableEventLogging(enableEventLogging bool) ProducerOption {
	return func(kp *Producer) { kp.IsAutoEventLogEnabled = enableEventLogging }
}

//NewKafkaProducer creates a new producer
//Following is the defaults for the kafka configuration
//		"go.batch.producer":                     true
//		"go.events.channel.size":                100000
//		"go.produce.channel.size":               100000
//		"max.in.flight.requests.per.connection": 1000000
//		"linger.ms":                             100
//		"queue.buffering.max.messages":          100000
//		"batch.num.messages":                    5000
//		"acks":                                  "1"
//You can change the defaults by sending a map to the SetCustomConfig Option
func NewKafkaProducer(brokerServers string, options ...ProducerOption) *Producer {
	kp := &Producer{
		CloseChannel:          make(chan os.Signal, 1),
		IsAutoEventLogEnabled: false,
	}
	signal.Notify(kp.CloseChannel, syscall.SIGINT, syscall.SIGTERM)

	kp.config = &kafka.ConfigMap{
		"bootstrap.servers":                     brokerServers,
		"go.batch.producer":                     true,
		"go.events.channel.size":                100000,
		"go.produce.channel.size":               100000,
		"max.in.flight.requests.per.connection": 1000000,
		"linger.ms":                             100,
		"queue.buffering.max.messages":          100000,
		"batch.num.messages":                    5000,
		"acks":                                  "1",
	}

	for _, option := range options {
		option(kp)
	}

	if kp.logger == nil {
		kp.logger = gologger.NewLogger()
	}

	producer, err := kafka.NewProducer(kp.config)
	if err != nil {
		kp.logger.LogError("Failed to create producer: %s\n", err)
		panic("Failed to create producer")
	}
	kp.producer = producer
	kp.publishChannel = producer.ProduceChannel()
	kp.EventsChannel = producer.Events()
	kp.logger.LogInfo(fmt.Sprintf("Created Producer"))
	kp.startEventLogging()
	kp.setGracefulCleaning()
	return kp
}
