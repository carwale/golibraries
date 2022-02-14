package kafka

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/confluentinc/confluent-kafka-go/kafka"
)

// RawEvent holds the message in byte form
type RawEvent []byte

// Message the message that is published to kafka
type Message struct {
	Data           RawEvent
	TopicPartition kafka.TopicPartition
	Timestamp      time.Time
}

var consumerInstanceCount int

// IProcessor : interface for consuming messages from queue
type IProcessor interface {
	ProcessMessage(*Message) bool
}

// Consumer holds the configuration for kafka consumers
type Consumer struct {
	InstanceID                      string
	logger                          *gologger.CustomLogger
	config                          *kafka.ConfigMap
	BrokerServers                   string
	Topics                          []string
	ConsumerGroupName               string
	Consumer                        *kafka.Consumer
	CloseChannel                    chan os.Signal
	enableDL                        bool
	dlConsumer                      *DLConsumer
	RetryCount                      int           // default to 5
	RetryDuration                   time.Duration // default to 24 hours
	offsetCommitMessageInterval     int           // default to 1000
	lastOffsetCommitMessageInterval int
	ReplayMode                      bool
	ReplayFrom                      time.Duration //duration - defaults to 1h
	ReplayType                      ReplayType
	ReplyCompletionChannel          chan bool
}

func (kc *Consumer) applyCustomConfig(customConfig map[string]interface{}) {
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

// ForceCommitOffset Methods actually call kafka commit offset API
func (kc *Consumer) ForceCommitOffset() {
	kc.Consumer.Commit()
}

func (kc *Consumer) commitOffset() {
	kc.lastOffsetCommitMessageInterval = (kc.lastOffsetCommitMessageInterval + 1) % kc.offsetCommitMessageInterval
	if kc.lastOffsetCommitMessageInterval == 0 {
		kc.ForceCommitOffset()
	}
}

// ConsumerOption sets a parameter for the KafkaProducer
type ConsumerOption func(l *Consumer)

// SetConsumerCustomConfig sets the custom config for kafka
func SetConsumerCustomConfig(customConfig map[string]interface{}) ConsumerOption {
	return func(kc *Consumer) {
		if customConfig != nil {
			for k, v := range customConfig {
				kc.config.SetKey(k, v)
			}
		}
	}
}

//ConsumerLogger sets the logger for consul
//Defaults to consul logger
func ConsumerLogger(customLogger *gologger.CustomLogger) ConsumerOption {
	return func(kc *Consumer) { kc.logger = customLogger }
}

// EnableDeadLettering Method to enable deadlettering
func EnableDeadLettering() ConsumerOption {
	return func(kc *Consumer) { kc.enableDL = true }
}

// EnableReplayMode Method to enable replaymode
// When enabling replaymode you need to pass the following
// ReplayType - this can be timestamp of beginning
// ReplayFrom - the duration before the current time from which you need to process the message.
//this is only considered in timestamp mode
func EnableReplayMode(replayType ReplayType, replayFrom string, replyCompletionChannel chan bool) ConsumerOption {
	return func(kc *Consumer) {
		kc.ReplayMode = true
		kc.ReplyCompletionChannel = replyCompletionChannel
		kc.ReplayType = replayType
		if replayType == TIMESTAMP {
			parsedDuration, err := time.ParseDuration(replayFrom)
			if err != nil {
				log.Fatalf("Duration for consumer was not parsed")
			}
			kc.ReplayFrom = parsedDuration
		}
	}
}

// SetOffsetCommitMessageInterval sets the offset commit message interval. The interval should be positive
// If it is not positive it will be set to default of 1000
func SetOffsetCommitMessageInterval(msgInterval int) ConsumerOption {
	return func(kc *Consumer) {
		if msgInterval > 0 {
			kc.offsetCommitMessageInterval = msgInterval
		}
	}
}

// NewKafkaConsumer Initialize a KafkaConsumer for provided configuration
// It will initialize with the following defaults
// offsetCommitMessageInterval: 1000
// lastOffsetCommitMessageInterval: 0
// enableDL: false
// broker.address.family: v4
// session.timeout.ms: 6000
// enable.auto.commit: false
// auto.offset.reset: earliest
// ReplayMode: false
// ReplayType: timestamp
// ReplayFrom: 1h
func NewKafkaConsumer(brokerServers string, consumerGroupName string, topics []string, options ...ConsumerOption) *Consumer {
	kc := &Consumer{
		Topics:                          topics,
		CloseChannel:                    make(chan os.Signal, 1),
		offsetCommitMessageInterval:     1000,
		ConsumerGroupName:               consumerGroupName,
		BrokerServers:                   brokerServers,
		lastOffsetCommitMessageInterval: 0,
		ReplayMode:                      false,
		ReplayType:                      TIMESTAMP,
		ReplayFrom:                      time.Duration(1 * time.Hour),
	}
	consumerInstanceCount++
	kc.InstanceID = fmt.Sprintf("%s-instance-%d", consumerGroupName, consumerInstanceCount)
	signal.Notify(kc.CloseChannel, syscall.SIGINT, syscall.SIGTERM)

	kc.config = &kafka.ConfigMap{
		"bootstrap.servers":        brokerServers,
		"broker.address.family":    "v4",
		"group.id":                 consumerGroupName,
		"session.timeout.ms":       6000,
		"enable.auto.commit":       false,
		"auto.offset.reset":        "earliest",
		"go.events.channel.enable": true,
		"enable.partition.eof":     true,
	}

	for _, option := range options {
		option(kc)
	}

	if kc.logger == nil {
		kc.logger = gologger.NewLogger()
	}
	if kc.ReplayMode {
		kc.config.SetKey("go.application.rebalance.enable", true)
	}
	c, err := kafka.NewConsumer(kc.config)
	if err != nil {
		kc.logger.LogError(fmt.Sprintf("Failed to create  %s", kc.InstanceID), err)
		panic(fmt.Sprintf("Failed to create %s: %s", kc.InstanceID, err))
	}
	kc.Consumer = c
	kc.logger.LogInfo(fmt.Sprintf("Created %s: %v", kc.InstanceID, c))
	return kc
}

func (kc *Consumer) startDeadLetteringConsumer(processor IProcessor) {
	if kc.enableDL {
		kc.dlConsumer = NewKafkaDLConsumer(kc.BrokerServers, fmt.Sprintf("%s-%s", kc.ConsumerGroupName, "dlq"), nil, kc.logger)
		if kc.RetryCount > 0 {
			// Setting RetryCount only when retry count is greater than 0
			kc.dlConsumer.RetryCount = kc.RetryCount
			kc.logger.LogDebug(fmt.Sprintf("Setting RetryCount to %d", kc.RetryCount))
		} else {
			panic("Retry count should be a positive value")
		}
		if kc.RetryDuration >= 5*time.Minute {
			// Setting Retry duration only if greater than 5 minutes
			kc.dlConsumer.RetryDuration = kc.RetryDuration
			kc.logger.LogDebug(fmt.Sprintf("Setting RetryDuration to %s", kc.RetryDuration))
		} else {
			panic("Retry duration cannot be less than 5 minutes")
		}
		dlTopics := []string{}
		for _, topic := range kc.Topics {
			dlTopics = append(dlTopics, fmt.Sprintf("%s-%s", topic, "DLQ"))
		}
		kc.dlConsumer.Topics = dlTopics
		go func() {
			kc.dlConsumer.Start(processor)
		}()
	}
}

//Start starts the consumer with the settings applied while creating the consumer
func (kc *Consumer) Start(processor IProcessor) {
	if len(kc.Topics) == 0 {
		kc.logger.LogErrorWithoutError(fmt.Sprintf("No topic subscribed for %s", kc.InstanceID))
	}
	err := kc.Consumer.SubscribeTopics(kc.Topics, nil)
	if err != nil {
		kc.logger.LogError(fmt.Sprintf("Error in topic Subscription for %s:", kc.InstanceID), err)
	}
	// If DeadLettering is enable Start the Kafaka DLConsumer
	kc.logger.LogWarning("Consumer started for topic: " + kc.Topics[0])
	kc.startDeadLetteringConsumer(processor)
	consumerStartTime := time.Now()
consumeloop:
	for {
		select {
		case sig := <-kc.CloseChannel:
			if kc.enableDL {
				kc.dlConsumer.CloseChannel <- sig
			}
			kc.logger.LogWarning(fmt.Sprintf("Caught signal %v in consumeloop : %s terminating ", sig, kc.InstanceID))
			kc.ForceCommitOffset()
			break consumeloop
		case ev := <-kc.Consumer.Events():
			if ev == nil {
				continue
			}
			shouldBreak := kc.processEvent(ev, processor, consumerStartTime)
			if shouldBreak {
				break consumeloop
			}
		}
	}
	kc.logger.LogWarning(fmt.Sprintf("Closing %s", kc.InstanceID))
	kc.Consumer.Close()
	if kc.ReplayMode {
		kc.ReplyCompletionChannel <- true
	}
}

//processEvent processes a kafka consumer event. It returns true if the consumer needs to stop
func (kc *Consumer) processEvent(ev kafka.Event, processor IProcessor, consumerStartTime time.Time) bool {
	var err error
	switch e := ev.(type) {
	case *kafka.Message:
		if kc.ReplayMode {
			if e.Timestamp.After(consumerStartTime) {
				return true
			}
		}
		processor.ProcessMessage(&Message{Data: e.Value, TopicPartition: e.TopicPartition, Timestamp: e.Timestamp})
		//kc.logger.LogDebug(fmt.Sprintf("Message on %s %s: %s Headers: %v", kc.InstanceID,
		//	e.TopicPartition, string(e.Value), e.Headers))
		kc.commitOffset()
	case kafka.Error:
		// Errors should generally be considered
		// informational, the client will try to
		// automatically recover.
		kc.logger.LogError(fmt.Sprintf("Error in %s: %v", kc.InstanceID, e.Code()), e)
		if e.Code() == kafka.ErrUnknownTopicOrPart {
			kc.logger.LogErrorWithoutError("error is fatal. Exiting")
			return true
		}

	case kafka.AssignedPartitions:
		partitionsToAssign := e.Partitions
		if len(partitionsToAssign) == 0 {
			kc.logger.LogErrorWithoutError("No partitions assigned\n")
			return false
		}

		kc.logger.LogWarning("Assigned/Re-assigned Partitions: " + kc.getPartitionNumbers(partitionsToAssign))
		//if the consumer was launched in replay mode, it needs to figure out which offset to replay from in each assigned partition, and then
		//reset the offset to that point for each partition.
		if kc.ReplayMode {
			switch kc.ReplayType {
			case BEGINNING:
				kc.logger.LogWarning("Replay from beginning, resetting offsets to beginning")
				//reset offsets of all assigned partitions to "beginning"
				partitionsToAssign, err = kc.resetPartitionOffsetsToBeginning(e.Partitions)
				if err != nil {
					kc.logger.LogError("error trying to reset offsets to beginning: %v", err)
				}
			case TIMESTAMP:
				timeFromConsumerStart := time.Now().Add(-kc.ReplayFrom)
				kc.logger.LogErrorWithoutError(fmt.Sprintf("Replay from timestamp %s, resetting offsets to that point", timeFromConsumerStart))
				if err != nil {
					kc.logger.LogError(fmt.Sprintf("failed to parse replay timestamp %s due to error", timeFromConsumerStart), err)
				}
				//reset offsets of all assigned partitions to the specified timestamp in the past
				partitionsToAssign, err = kc.resetPartitionOffsetsToTimestamp(e.Partitions, timeFromConsumerStart.UnixNano()/int64(time.Millisecond))
				if err != nil {
					kc.logger.LogError("error trying to reset offsets to timestamp: ", err)
				}
			}
		}

		kc.Consumer.Assign(partitionsToAssign)
	case kafka.RevokedPartitions:
		kc.Consumer.Unassign()
	case kafka.PartitionEOF:
		kc.logger.LogWarning("Reached End of partition")
		if kc.ReplayMode {
			return true
		}
	default:
		kc.logger.LogDebug(fmt.Sprintf("Ignored %s: %v", kc.InstanceID, e))

	}
	return false
}

func (kc *Consumer) resetPartitionOffsetsToTimestamp(partitions []kafka.TopicPartition, timestamp int64) ([]kafka.TopicPartition, error) {
	var prs []kafka.TopicPartition
	for _, par := range partitions {
		prs = append(prs, kafka.TopicPartition{Topic: par.Topic, Partition: par.Partition, Offset: kafka.Offset(timestamp)})
	}

	updtPars, err := kc.Consumer.OffsetsForTimes(prs, 5000)
	if err != nil {
		kc.logger.LogError("Failed to reset offsets to supplied timestamp due to error: %v\n", err)
		return partitions, err
	}

	return updtPars, nil
}

func (kc *Consumer) resetPartitionOffsetsToBeginning(partitions []kafka.TopicPartition) ([]kafka.TopicPartition, error) {
	var prs []kafka.TopicPartition
	for _, par := range partitions {
		prs = append(prs, kafka.TopicPartition{Topic: par.Topic, Partition: par.Partition, Offset: kafka.OffsetBeginning})
	}

	return prs, nil
}

func (kc *Consumer) getPartitionNumbers(pars []kafka.TopicPartition) string {
	var pNums string
	for i, par := range pars {
		if i == len(pars)-1 {
			pNums = pNums + strconv.Itoa(int(par.Partition))
		} else {
			pNums = pNums + strconv.Itoa(int(par.Partition)) + ", "
		}
	}

	return pNums
}
