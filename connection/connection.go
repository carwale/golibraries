package connection

import (
	"fmt"
	"time"

	"github.com/carwale/golibraries/gologger"

	"github.com/streadway/amqp"
)

// Provider is a placeholder struct for implementing the IConnectionProvider interface.
type Provider struct {
}

var uclogger *gologger.CustomLogger

// NewConnection provides a new rabbitmq connection, retries for up to 30 minutes in case of failure
func (provider *Provider) NewConnection(server string, logger *gologger.CustomLogger) (*amqp.Connection, error) {
	var connection *amqp.Connection

	var err error

	uclogger = logger

	connectDelay := 1 // 1 second

	maxDelay := 1800 //30 minutes

	uri := "amqp://guest:guest@" + server

	for {
		connection, err = amqp.Dial(uri)

		if err != nil {
			uclogger.LogError("error while connecting to rabbitmq", err)

			if connectDelay < maxDelay {
				connectDelay *= 2
			} else {
				return nil, fmt.Errorf("max delay reached while trying to establish rabbitmq connection to %s", server)
			}

			time.Sleep(time.Duration(connectDelay) * time.Second)
		} else {
			uclogger.LogDebug("connection established successfully")

			return connection, nil
		}
	}

}
