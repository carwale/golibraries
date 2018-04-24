package channelprovider

import (
	"fmt"
	"sync"
	"time"

	"github.com/carwale/golibraries/gologger"

	"github.com/carwale/golibraries/rabbitmq/connection"
	"github.com/carwale/golibraries/rabbitmq/connectionpool"
	"github.com/spf13/viper"
	"github.com/streadway/amqp"
)

var once sync.Once
var channelPro *ChannelProvider

// ChannelProvider is container for logger and connection pool, has method to get channel.
type ChannelProvider struct {
	uclogger *gologger.CustomLogger
	pool     *connectionpool.Pool
}

//NewChannelProvider gives you a new channel provider. It takes the list of servers from "rabbitmq" in config
func NewChannelProvider(logger *gologger.CustomLogger) *ChannelProvider {
	return NewChannelProviderWithServers(logger, viper.GetStringSlice("rabbitmq"))
}

//NewChannelProviderWithServers gives you a new channel provider. You have to pass a list of rabbitmq servers.
func NewChannelProviderWithServers(logger *gologger.CustomLogger, rabbitMqServers []string) *ChannelProvider {

	once.Do(func() {
		serverList := rabbitMqServers
		channelPro = &ChannelProvider{
			pool:     connectionpool.NewConnectionPool(&serverList, &connection.Provider{}, logger),
			uclogger: logger,
		}

	})
	return channelPro
}

// GetChannel creates and returns a channel
func (cp *ChannelProvider) GetChannel() (*amqp.Channel, error) {

	if cp.pool == nil {
		return nil, fmt.Errorf("connection pool is not initialised")
	}

	connectDelay := 1 // 1 second

	maxDelay := 1800 // 30 minutes

	for {

		connection, err := cp.pool.GetConnection()

		if err != nil {
			cp.uclogger.LogError("Error getting connection from pool", err)
			continue
		}

		channel, err := connection.Channel()

		if err != nil {
			cp.uclogger.LogError("error creating channel", err)

			if connectDelay < maxDelay {
				connectDelay *= 2
			} else {
				return nil, fmt.Errorf("max delay reached while trying to get channel")
			}
			time.Sleep(time.Duration(connectDelay) * time.Second)
		} else {
			return channel, nil
		}
	}
}
