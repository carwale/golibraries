package kafka

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/confluentinc/confluent-kafka-go/kafka"
)

var dlConsumerInstanceCount int

//DLConsumer holds the configuration for the DL consumer
type DLConsumer struct {
	InstanceID                      string
	logger                          *gologger.CustomLogger
	config                          *kafka.ConfigMap
	BrokerServers                   string
	Topics                          []string
	ConsumerGroupName               string
	Consumer                        *kafka.Consumer
	CloseChannel                    chan os.Signal
	RetryCount                      int           // default to 5
	RetryDuration                   time.Duration // default to 24 hours
	processor                       IProcessor
	partitions                      []kafka.TopicPartition
	partitionMessages               [12]*kafka.Message // At max only 12 partition to be allowed for Dead Letter
	tickMillisecond                 int
	offsetCommitMessageInterval     int // default to 1000
	lastOffsetCommitMessageInterval int
}

func (kc *DLConsumer) applyCustomConfig(customConfig map[string]interface{}) {
	if customConfig != nil {
		for k, v := range customConfig {
			if k != "enable.auto.commit" {
				kc.config.SetKey(k, v)
			} else {
				kc.logger.LogWarning("Offset auto commit is disabled. Please use `SetOffsetCommitMessageInterval` method to set message interval between subsequent offset commit.")
			}
		}
	}
}

// NewKafkaDLConsumer Initialize a DLConsumer for provided configuration
func NewKafkaDLConsumer(brokerServers string, consumerGroupName string, customConfig map[string]interface{}, logger *gologger.CustomLogger) *DLConsumer {
	kc := &DLConsumer{
		CloseChannel: make(chan os.Signal, 1),
	}
	signal.Notify(kc.CloseChannel, syscall.SIGINT, syscall.SIGTERM)
	dlConsumerInstanceCount++
	kc.InstanceID = fmt.Sprintf("%s-instance-%d", consumerGroupName, dlConsumerInstanceCount)
	kc.logger = logger
	kc.config = &kafka.ConfigMap{
		"bootstrap.servers":     brokerServers,
		"broker.address.family": "v4",
		"group.id":              consumerGroupName,
		"session.timeout.ms":    6000,
		"enable.auto.commit":    false,
		"auto.offset.reset":     "earliest",
	}
	kc.RetryCount = 5
	kc.RetryDuration = time.Duration(24) * time.Hour
	kc.applyCustomConfig(customConfig)
	c, err := kafka.NewConsumer(kc.config)
	if err != nil {
		kc.logger.LogError(fmt.Sprintf("Failed to create  %s", kc.InstanceID), err)
		panic(fmt.Sprintf("Failed to create %s: %s", kc.InstanceID, err))
	}
	kc.Consumer = c
	kc.logger.LogInfo(fmt.Sprintf("Created %s: %v", kc.InstanceID, c))
	return kc
}

// SubscribeTopic suscribes to a list of topics
func (kc *DLConsumer) SubscribeTopic(topics []string) {
	kc.Topics = topics
	kc.logger.LogInfo(fmt.Sprintf("%s subscribed to topics %v", kc.InstanceID, topics))
}

// GetPartitions returns partition
func (kc *DLConsumer) getPartitions() []kafka.TopicPartition {
	partitions, err := kc.Consumer.Assignment()
	kc.logger.LogDebug(fmt.Sprintf("Assigned partitions : %v", partitions))
	if err != nil {
		kc.logger.LogError("Error in getPartitions : ", err)
		return nil
	}
	return partitions
}

// GetPartitions sets and return partitions of the subscribed topic
func (kc *DLConsumer) GetPartitions() []kafka.TopicPartition {
	if kc.partitions != nil && len(kc.partitions) > 0 {
		return kc.partitions
	}
	kc.partitions = kc.getPartitions()
	return kc.partitions
}

// GetPartitionCount return partitionCount of the subscribed topic
func (kc *DLConsumer) GetPartitionCount(topic string) int {
	metadata, err := kc.Consumer.GetMetadata(&topic, false, 1000)
	if err != nil {
		kc.logger.LogError(fmt.Sprintf("Error in GetPartitionCount while fetching metadata information for topic %s", topic), err)
		return 0
	}
	topicMetadata, ok := metadata.Topics[topic]
	if !ok {
		kc.logger.LogWarning(fmt.Sprintf("GetPartitionCount: topic %s not found in metadata", topic))
		return 0
	}
	return len(topicMetadata.Partitions)
}

// Checks whether the message published in the partition can be processed
func (kc *DLConsumer) isEligibleForProcess(msg *kafka.Message, partition int) bool {
	if msg != nil {
		initialInterval := int64(kc.RetryDuration) / int64(math.Pow(2, float64(kc.RetryCount))-1)
		return time.Now().Sub(msg.Timestamp) > time.Duration(initialInterval*int64(math.Pow(2, float64(partition))))
	}
	return false
}

// ReadPartition reads message from partition till timeoutMs or if message in partition can't be processed currently
func (kc *DLConsumer) ReadPartition(partition int, timeoutMs int64) {
	var err error
	var prevMsg *kafka.Message
	currentPartition := kc.GetPartitions()[partition]
	msg := kc.partitionMessages[partition]
	isCurrentMessageEligible := kc.isEligibleForProcess(msg, partition)
	kc.logger.LogDebug(fmt.Sprintf("Reading partition %s[%d] for timeout %d", *currentPartition.Topic, currentPartition.Partition, timeoutMs))
	// Check whether current partition message is eligible for processing then only switch consumer
	if msg == nil || isCurrentMessageEligible {
		err = kc.Consumer.Pause(kc.GetPartitions())
		if err != nil {
			kc.logger.LogWarning(fmt.Sprintf("Error in ReadPartition consumer pause - %s", err))
		}
		err = kc.Consumer.Resume([]kafka.TopicPartition{currentPartition})
		if err != nil {
			kc.logger.LogWarning(fmt.Sprintf("Error in ReadPartition consumer resume - %s", err))
		}
	}
	for {
		if isCurrentMessageEligible {
			kc.logger.LogDebug(fmt.Sprintf("Processing message with timestamp %s in topic %s[%d]: at %s", msg.Timestamp, *currentPartition.Topic, currentPartition.Partition, time.Now()))
			kc.processor.ProcessMessage(&Message{Data: msg.Value, TopicPartition: msg.TopicPartition})
		} else {
			// Offset of previos message commited when current message can't be processed
			if prevMsg != nil {
				kc.Consumer.CommitMessage(prevMsg)
				break
			}
			if msg != nil {
				break
			}
		}
		prevMsg = msg
		msg, err = kc.Consumer.ReadMessage(time.Duration(timeoutMs) * time.Millisecond)
		if err != nil {
			if err.(kafka.Error).Code() != kafka.ErrTimedOut {
				kc.logger.LogError(fmt.Sprintf("Error in ReadPartition topic %s[%d]:", *currentPartition.Topic, currentPartition.Partition), err)
			} else {
				if prevMsg != nil {
					kc.Consumer.CommitMessage(prevMsg)
				}
			}
			kc.logger.LogDebug(fmt.Sprintf("Tried reading topic %s[%d], %s", *currentPartition.Topic, currentPartition.Partition, err))
			break // Breaking if any error encountered while reading
		}
	}
	// Storing last unprocessed or nil message in partitionMessages
	kc.partitionMessages[partition] = msg
}

//ReadMessageFromPartitions reads message from partition with a timeout in milliseconds
func (kc *DLConsumer) ReadMessageFromPartitions(timeoutMs int) {
	for i := range kc.GetPartitions() {
		kc.ReadPartition(i, int64(timeoutMs/len(kc.partitions)))
	}
}

//Start starts the dl consumer
func (kc *DLConsumer) Start(processor IProcessor) {
	if len(kc.Topics) == 0 {
		kc.logger.LogErrorWithoutError(fmt.Sprintf("No topic subscribed for %s", kc.InstanceID))
	}
	err := kc.Consumer.SubscribeTopics(kc.Topics, nil)
	if err != nil {
		kc.logger.LogError(fmt.Sprintf("Error in topic Subscription for %s:", kc.InstanceID), err)
	}
	var unprocessedMessages []*kafka.Message
	for {
		msg, _ := kc.Consumer.ReadMessage(time.Duration(1000) * time.Millisecond)
		if msg != nil {
			unprocessedMessages = append(unprocessedMessages, msg)
		}
		parts := kc.GetPartitions()
		if len(parts) > 0 {
			for _, msg := range unprocessedMessages {
				if msg.TopicPartition.Partition < int32(kc.RetryCount) {
					processor.ProcessMessage(&Message{Data: msg.Value, TopicPartition: msg.TopicPartition})
				}
			}
			// Committing currently read messages
			kc.Consumer.Commit()
			break
		}
	}

	for _, topic := range kc.Topics {
		if kc.RetryCount >= kc.GetPartitionCount(topic) {
			panic(fmt.Sprintf("%s topic has unsufficent partition. Ensure partition greater than retry count %d", topic, kc.RetryCount))
		}
	}

	kc.processor = processor
	kc.tickMillisecond = 1000 * len(kc.GetPartitions())
	ticker := time.NewTicker(time.Duration(int64(kc.tickMillisecond)) * time.Millisecond)
consumeloop:
	for {
		select {
		case sig := <-kc.CloseChannel:
			kc.logger.LogWarning(fmt.Sprintf("Caught signal %v in consumeloop : %s terminating ", sig, kc.InstanceID))
			break consumeloop
		case <-ticker.C:
			kc.ReadMessageFromPartitions(kc.tickMillisecond)
		}
	}
	kc.logger.LogWarning(fmt.Sprintf("Closing %s", kc.InstanceID))
	kc.Consumer.Close()
}
