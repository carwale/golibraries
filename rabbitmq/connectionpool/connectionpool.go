package connectionpool

import (
	"fmt"
	"time"

	"github.com/carwale/golibraries/gologger"

	"github.com/streadway/amqp"
)

// Pool represents a pool of connections
type Pool struct {
	connections        map[string]*Container
	serverList         []string
	getConnection      chan *Container
	addConnection      chan *Container
	removeConnection   chan *Container
	connectionProvider IConnectionProvider
}

// IConnectionProvider defines the interface to be implemented by a connection provider.
type IConnectionProvider interface {
	NewConnection(string, string, string, gologger.ILogger) (*amqp.Connection, error)
}

// Container contains connection and related info
type Container struct {
	connection *amqp.Connection
	serverInfo string
}

var uclogger gologger.ILogger

// NewConnectionPool returns new connection pool, waits for 3 seconds before returning
func NewConnectionPool(serverList *[]string, username string, password string, connectionProvider IConnectionProvider, logger gologger.ILogger) *Pool {
	pool := &Pool{
		connections:        make(map[string]*Container),
		serverList:         *serverList,
		getConnection:      make(chan *Container),
		addConnection:      make(chan *Container),
		removeConnection:   make(chan *Container),
		connectionProvider: connectionProvider,
	}

	uclogger = logger
	for _, server := range *serverList {
		go pool.addNewConnection(server, username, password)
	}

	go func() {
		nextNodeIndex := 0
		for {
			var sendConnection chan *Container
			var nextConnection *Container
			if len(pool.connections) > 0 {
				sendConnection = pool.getConnection
				for nextConnection == nil {
					nextNodeIndex = (nextNodeIndex + 1) % len(*serverList)
					nextConnection = pool.connections[(*serverList)[nextNodeIndex]]
				}
			}

			select {
			case container := <-pool.addConnection:
				pool.connections[container.serverInfo] = container
			case container := <-pool.removeConnection:
				delete(pool.connections, container.serverInfo)
			case sendConnection <- nextConnection:
			}
		}
	}()

	return pool
}

// addNewConnection manages establishing new connection and adding it to pool,
// also listens for connection errors and retries connecting.
func (pool *Pool) addNewConnection(server string, username string, password string) {
	conn, err := pool.connectionProvider.NewConnection(server, username, password, uclogger)
	if err != nil {
		uclogger.LogError("could not establish rabbitmq connection", err)
		go pool.addNewConnection(server, username, password) // retry establishing connection
		return
	}

	errorChannel := make(chan *amqp.Error)
	conn.NotifyClose(errorChannel)

	container := &Container{
		connection: conn,
		serverInfo: server,
	}

	pool.addConnection <- container // send container to be added to pool

	go func() {
		conerr := <-errorChannel

		if conerr != nil {
			pool.removeConnection <- container // send container to be removed from pool
			uclogger.LogErrorWithoutError(fmt.Sprintf("Error in rabbitmq connection Code: %d Reason: %q, Server: %s", conerr.Code, conerr.Reason, server))
			pool.addNewConnection(server, username, password)
		}
	}()
}

// GetConnection provides a rabbitmq connection from connection pool, times out in 1 minute if unable to get a connection
func (pool *Pool) GetConnection() (*amqp.Connection, error) {
	select {
	case container := <-pool.getConnection:
		return container.connection, nil
	case <-time.After(1 * time.Minute):
		err := fmt.Errorf("timeout occurred while trying to get a connection")
		uclogger.LogError("error while trying to get connection from pool", err)
		return nil, err
	}
}
