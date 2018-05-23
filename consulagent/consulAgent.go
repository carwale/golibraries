package consulagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/goutilities"
	"github.com/hashicorp/consul/api"
)

//IServiceDiscoveryAgent is the interface that every service discovery agent
//should implement
type IServiceDiscoveryAgent interface {
	//RegisterService will register the service given the name, ip and port
	//It returns the ID of the service
	RegisterService(name, ipAddress, port string) (string, error)
	//DeregisterService will deregister the service given the ID
	DeregisterService(serviceID string)
	//GetHealthyService will give a list of all the instances of the module
	GetHealthyService(moduleName string) ([]string, error)
}

// ConsulAgent is the custom consul agent that will be used by all go lang applications
type ConsulAgent struct {
	consulHostName          string
	consulPortNumber        int
	consulMonScriptName     string
	consulServiceScriptName string
	consulAgent             *api.Client
	logger                  *gologger.CustomLogger
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

//ConsulMonScriptName sets the name of the mon check script for the service
//The script should be located in the mon folder of the application
//Defaults to mon.py
func ConsulMonScriptName(name string) Options {
	return func(c *ConsulAgent) {
		if name != "" {
			c.consulMonScriptName = name
		}
	}
}

//ConsulServiceScriptName sets the name of the service check script
//The script should be located in the mon folder of the application
//Defaults to consultest.py
func ConsulServiceScriptName(name string) Options {
	return func(c *ConsulAgent) {
		if name != "" {
			c.consulServiceScriptName = name
		}
	}
}

//Logger sets the logger for consul
//Defaults to consul logger
func Logger(customLogger *gologger.CustomLogger) Options {
	return func(c *ConsulAgent) { c.logger = customLogger }
}

//NewConsulAgent will initialize consul client.
func NewConsulAgent(options ...Options) IServiceDiscoveryAgent {

	c := &ConsulAgent{
		consulHostName:          "127.0.0.1",
		consulPortNumber:        8500,
		consulMonScriptName:     "mon.py",
		consulServiceScriptName: "consultest.py",
		logger:                  gologger.NewLogger(),
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

//RegisterService will register the service on consul
//It will also register two checks for the service. A mon check and a consultest check
//mon check can be used for releases while the service check script should check
//whether the service is running or not.
func (c *ConsulAgent) RegisterService(name, ipAddress, port string) (string, error) {
	consulServiceName := name
	gatewayPort, err := strconv.Atoi(port[1:])
	if err != nil {
		c.logger.LogError("Could not convert port from string to int", err)
	}
	hostName, err := os.Hostname()
	if err != nil {
		c.logger.LogError("Could not get hostname", err)
		hostName = goutilities.RandomString(6)
	}
	serviceID, err := c.registerServiceOnConsul(consulServiceName, ipAddress, hostName, gatewayPort)
	if err != nil {
		c.logger.LogError(fmt.Sprintf("Could not register %s on consul", consulServiceName), err)
		panic(fmt.Errorf("Could not register %s on consul", consulServiceName))
	}
	workingDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		c.logger.LogWarning("Could not get working directory. Setting it as current directory" + err.Error())
		workingDir = "."
	}
	monScriptLocation := workingDir + string(os.PathSeparator) + "mon" + string(os.PathSeparator) + c.consulMonScriptName
	serviceScriptLocation := workingDir + string(os.PathSeparator) + "mon" + string(os.PathSeparator) + c.consulServiceScriptName
	err = nil
	ok := c.registerCheck(serviceID, "checkMon", name+" check mon", monScriptLocation)
	if !ok {
		err = errors.New("Could not register consul service check")
	}
	ok = c.registerCheck(serviceID, "checkService", name+" check service", serviceScriptLocation)
	if !ok {
		err = errors.New("Could not register consul service check")
	}
	return serviceID, err
}

func (c *ConsulAgent) registerServiceOnConsul(name, ipAddress, hostName string, port int) (string, error) {
	serviceID := name + "-" + hostName + "-" + strconv.Itoa(port)
	err := c.consulAgent.Agent().ServiceRegister(&api.AgentServiceRegistration{
		Name:    name,
		ID:      serviceID,
		Address: ipAddress,
		Port:    port,
	},
	)
	if err != nil {
		c.logger.LogError("Error registering service in consul", err)
		return "", err
	}
	return serviceID, nil
}

func (c *ConsulAgent) registerCheck(serviceID, checkID, checkName, scriptLocation string) bool {
	err := c.consulAgent.Agent().CheckRegister(&api.AgentCheckRegistration{
		ID:        serviceID + checkID,
		Name:      checkName,
		ServiceID: serviceID,
		AgentServiceCheck: api.AgentServiceCheck{
			Script:   scriptLocation,
			Interval: "10s",
			Timeout:  "5s",
		},
	})
	if err != nil {
		c.logger.LogError("Error registering service check in consul", err)
		return false
	}
	return true
}

//DeregisterService will deregister all the checks and the service itself
//This should be used on an exit listener of the application. It will help
//reduce clutter in consul
func (c *ConsulAgent) DeregisterService(serviceID string) {
	err := c.consulAgent.Agent().ServiceDeregister(serviceID)
	if err != nil {
		c.logger.LogError("Error deregistering service in consul", err)
	}
}

//GetHealthyService will give all the IPs of the service
func (c *ConsulAgent) GetHealthyService(moduleName string) ([]string, error) {
	res, _, err := c.consulAgent.Health().Service(moduleName, "", true, nil)
	if err != nil {
		c.logger.LogError("Error getting healthy IP Addresses for module "+moduleName+" from consul", err)
		return nil, err
	}
	ipAddList := make([]string, 0)
	if len(res) == 0 {
		err = errors.New("No healthy instance of module " + moduleName + " found")
		c.logger.LogError("No instance found for module "+moduleName+" from consul", err)
		return ipAddList, err
	}
	for _, val := range res {
		address := val.Service.Address
		port := val.Service.Port
		ipAddList = append(ipAddList, address+":"+strconv.Itoa(port))
	}
	return ipAddList, nil
}
