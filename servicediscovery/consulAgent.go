package servicediscovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/carwale/golibraries/healthcheck"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/goutilities"
	"github.com/hashicorp/consul/api"
)

// ConsulAgent is the custom consul agent that will be used by all go lang applications
type ConsulAgent struct {
	consulHostName      string
	consulPortNumber    int
	consulMonScriptName string
	consulAgent         *api.Client
	logger              *gologger.CustomLogger
}

// Options sets a parameter for consul agent
type Options func(c *ConsulAgent)

// ConsulHost sets the IP for consul agent. Defults to 127.0.0.1
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

// ConsulMonScriptName sets the name of the mon check script for the service
// The script should be located in the mon folder of the application
// Defaults to mon.py
func ConsulMonScriptName(name string) Options {
	return func(c *ConsulAgent) {
		if name != "" {
			c.consulMonScriptName = name
		}
	}
}

// Logger sets the logger for consul
// Defaults to consul logger
func Logger(customLogger *gologger.CustomLogger) Options {
	return func(c *ConsulAgent) { c.logger = customLogger }
}

// NewConsulAgent will initialize consul client.
func NewConsulAgent(options ...Options) IServiceDiscoveryAgent {

	c := &ConsulAgent{
		consulHostName:      "127.0.0.1",
		consulPortNumber:    8500,
		consulMonScriptName: "mon.py",
		logger:              gologger.NewLogger(),
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

// RegisterService will register the service on consul
// It will also register two checks for the service. A mon check and a gRPC check
// mon check can be used for releases while the gRPC service check script should check
// whether the service is running or not.
func (c *ConsulAgent) RegisterService(name, ipAddress, port, healthCheckPort string, checkFunction func() (bool, error), isDockerType bool, tags []string, metadata map[string]string) (string, error) {
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
	serviceID, err := c.registerServiceOnConsul(consulServiceName, ipAddress, hostName, gatewayPort, tags, metadata)
	if err != nil {
		c.logger.LogError(fmt.Sprintf("Could not register %s on consul", consulServiceName), err)
		panic(fmt.Errorf("could not register %s on consul", consulServiceName))
	}
	workingDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		c.logger.LogWarning("Could not get working directory. Setting it as current directory" + err.Error())
		workingDir = "."
	}
	err = nil
	if !isDockerType {
		monScriptLocation := workingDir + string(os.PathSeparator) + "mon" + string(os.PathSeparator) + c.consulMonScriptName
		ok := c.registerCheck(serviceID, "checkMon", name+" check mon", monScriptLocation)
		if !ok {
			err = errors.New("could not register consul service check")
		}
	}

	ok := c.registerGrpcCheck(serviceID, "checkService", name+" check service", ipAddress, healthCheckPort, checkFunction)
	if !ok {
		err = errors.New("could not register gRPC consul service check")
	}
	return serviceID, err
}

func (c *ConsulAgent) registerServiceOnConsul(name, ipAddress, hostName string, port int, tags []string, metadata map[string]string) (string, error) {
	serviceID := name + "-" + hostName + "-" + strconv.Itoa(port)
	err := c.consulAgent.Agent().ServiceRegister(&api.AgentServiceRegistration{
		Name:    name,
		ID:      serviceID,
		Address: ipAddress,
		Port:    port,
		Tags:    tags,
		Meta:    metadata,
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
			Args:                           []string{"python", scriptLocation},
			Interval:                       "10s",
			Timeout:                        "5s",
			DeregisterCriticalServiceAfter: "24h",
		},
	})
	if err != nil {
		c.logger.LogError("Error registering service check in consul", err)
		return false
	}
	return true
}

func (c *ConsulAgent) registerGrpcCheck(serviceID, checkID, checkName, ipAddress, healthCheckPort string, checkFunction func() (bool, error)) bool {
	healthcheck.NewHealthCheckServer(healthCheckPort, checkFunction, healthcheck.Logger(c.logger))
	err := c.consulAgent.Agent().CheckRegister(&api.AgentCheckRegistration{
		ID:        serviceID + checkID,
		Name:      checkName,
		ServiceID: serviceID,
		AgentServiceCheck: api.AgentServiceCheck{
			GRPC:                           ipAddress + healthCheckPort,
			Interval:                       "10s",
			Timeout:                        "1s",
			DeregisterCriticalServiceAfter: "24h",
			GRPCUseTLS:                     false,
		},
	})
	if err != nil {
		c.logger.LogError("Error registering service check in consul", err)
		return false
	}
	return true
}

// DeregisterService will deregister all the checks and the service itself
// This should be used on an exit listener of the application. It will help
// reduce clutter in consul
func (c *ConsulAgent) DeregisterService(serviceID string) {
	err := c.consulAgent.Agent().ServiceDeregister(serviceID)
	if err != nil {
		c.logger.LogError("Error deregistering service in consul", err)
	}
}

// GetHealthyService will give all the IPs of the service
func (c *ConsulAgent) GetHealthyService(moduleName string, k8sNamespace string) ([]string, error) {
	res, _, err := c.consulAgent.Health().Service(moduleName, k8sNamespace, true, nil)
	ipAddList := make([]string, 0)
	if err != nil {
		c.logger.LogError("Error getting healthy IP Addresses for module "+moduleName+" from consul for namespace"+k8sNamespace, err)
		return nil, err
	}
	if len(res) == 0 {
		res, _, err = c.consulAgent.Health().Service(moduleName, "", true, nil)
		if err != nil {
			c.logger.LogError("Error getting healthy IP Addresses for module "+moduleName+" from consul", err)
			return nil, err
		}
		if len(res) == 0 {
			err = errors.New("No healthy instance of module " + moduleName + " found")
			c.logger.LogInfo("No instance found for module " + moduleName + " from GetHealthyService")
			return ipAddList, err
		}
	}
	for _, val := range res {
		address := val.Service.Address
		port := val.Service.Port
		ipAddList = append(ipAddList, address+":"+strconv.Itoa(port))
	}
	return ipAddList, nil
}

// GetHealthyServiceWithZoneInfo will give all the IPs of the service and other info like zones
func (c *ConsulAgent) GetHealthyServiceWithZoneInfo(moduleName string, k8sNamspace string) ([]EndpointsWithExtraInfo, error) {
	ipAddList := make([]EndpointsWithExtraInfo, 0)
	res, _, err := c.consulAgent.Health().Service(moduleName, k8sNamspace, true, nil)
	if err != nil {
		c.logger.LogError("Error getting healthy IP Addresses for module "+moduleName+" from consul for namespace"+k8sNamspace, err)
		return nil, err
	}
	if len(res) == 0 {
		res, _, err = c.consulAgent.Health().Service(moduleName, "", true, nil)
		if err != nil {
			c.logger.LogError("Error getting healthy IP Addresses for module "+moduleName+" from consul", err)
			return nil, err
		}
		if len(res) == 0 {
			err = errors.New("No healthy instance of module " + moduleName + " found")
			c.logger.LogInfo("No instance found for module " + moduleName + " from GetHealthyServiceWithZoneInfo")
			return ipAddList, err
		}
	}
	for _, val := range res {
		address := val.Service.Address
		port := val.Service.Port
		ipAddList = append(ipAddList, EndpointsWithExtraInfo{
			Address: address + ":" + strconv.Itoa(port),
			Zone:    "",
		})
	}
	return ipAddList, nil
}
