# Helper packages for connecting to rabbitmq


## Package [channelprovider](./channelprovider/channelprovider.go)
`NewChannelProvider` gives you a new channel provider. It takes the list of servers from "rabbitmq" in config.

`NewChannelProviderWithServers` gives you a new channel provider. You have to pass a list of rabbitmq servers.

`GetChannel` initialises a connection pool once and tries to get a channel, with exponential back-off up to 30 minutes.

## Package [connectionpool](./connectionpool/connectionpool.go)
`NewConnectionPool` allows to create a new connection pool (type `Pool`), manages adding/removing connection from pool. Also provides method to get connection from pool, which has a timeout of 1 minute.

## Package [connection](./connection/connection.go)
Provides method to get a new connection to the given server, will retry with exponential back-off upto 30 min. Implements the `IConnectionProvider`  interface defined in [connectionpool](./connectionpool/connectionpool.go)
