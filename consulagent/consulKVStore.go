package consulagent

import (
	"bytes"
	"encoding/gob"
	"strconv"

	"github.com/carwale/golibraries/gologger"
	"github.com/hashicorp/consul/api"
)

//ConsulAgent holds a singleton consul agent and a logger
type ConsulAgent struct {
	consulHostName   string
	consulPortNumber int
	consulAgent      *api.Client
	logger           *gologger.CustomLogger
}

// Options sets a parameter for consul agent
type Options func(c *ConsulAgent)

//ConsulHost sets the IP for consul agent. Defults to 127.0.0.1
func ConsulHost(hostName string) Options {
	return func(c *ConsulAgent) {
		if hostName != "" {
			c.consulHostName = hostName
		}
	}
}

// ConsulPort sets the port for consul agent. Defaults to 8500
func ConsulPort(portNumber int) Options {
	return func(c *ConsulAgent) {
		if portNumber != 0 {
			c.consulPortNumber = portNumber
		}
	}
}

//Logger sets the logger for consul
//Defaults to consul logger
func Logger(customLogger *gologger.CustomLogger) Options {
	return func(c *ConsulAgent) { c.logger = customLogger }
}

//NewConsulAgent will initialize consul client.
func NewConsulAgent(options ...Options) *ConsulAgent {

	c := &ConsulAgent{
		consulHostName:   "127.0.0.1",
		consulPortNumber: 8500,
		logger:           gologger.NewLogger(),
	}

	for _, option := range options {
		option(c)
	}

	client, err := api.NewClient(&api.Config{
		Address: c.consulHostName + ":" + strconv.Itoa(c.consulPortNumber),
	})
	if err != nil {
		c.logger.LogError("could not connect to consul!!", err)
		panic("could not connect to consul")
	}
	c.consulAgent = client
	return c
}

// GetKeys gets the list of keys for the prefix string
func (ca *ConsulAgent) GetKeys(prefix string) []string {
	pairs, _, err := ca.consulAgent.KV().Keys(prefix, "", nil)
	if err != nil {
		ca.logger.LogError("Error getting keys for prefix "+prefix, err)
	}
	return pairs
}

// GetKeyValuePairs gets the list of keys and corresponding values for a prefix string
func (ca *ConsulAgent) GetKeyValuePairs(prefix string) map[string][]byte {
	pairs, _, err := ca.consulAgent.KV().List(prefix, nil)
	if err != nil {
		ca.logger.LogError("Error getting keys for prefix "+prefix, err)
	}
	var resMap = make(map[string][]byte)
	for _, pair := range pairs {
		resMap[pair.Key] = pair.Value
	}
	return resMap
}

// GetValue gets the value of the key
func (ca *ConsulAgent) GetValue(key string) []byte {
	pair, _, err := ca.consulAgent.KV().Get(key, nil)
	if err != nil {
		ca.logger.LogError("Error getting value for key "+key, err)
		return nil
	}
	if err != nil {
		ca.logger.LogError("Error getting value for key "+key, err)
		return nil
	}
	return pair.Value
}

// CreateKV creates a key value pair
func (ca *ConsulAgent) CreateKV(key string, value interface{}) bool {
	var err error
	valueBytes, ok := value.([]byte)
	if !ok {
		valueBytes, err = getBytes(value)
	}
	if err != nil {
		ca.logger.LogError("Could not create KV Pair as the value could not be converted to bytes for key "+key, err)
		return false
	}
	p := &api.KVPair{Key: key, Value: valueBytes}
	_, err = ca.consulAgent.KV().Put(p, nil)
	if err != nil {
		ca.logger.LogError("Error creating kv pair with key "+key, err)
	}
	return true
}

// DeleteKV creates a key value pair
func (ca *ConsulAgent) DeleteKV(key string) bool {
	_, err := ca.consulAgent.KV().Delete(key, nil)
	if err != nil {
		ca.logger.LogError("Error getting value for key "+key, err)
		return false
	}
	return true
}

func getBytes(key interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(key)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
