package servicediscovery

import (
	"fmt"
)

type multiClient struct {
	clients []IServiceDiscoveryAgent
}

// NewMultiSourceClient returns new K8s Service discovery agent
func NewMultiSourceClient(clients ...IServiceDiscoveryAgent) IServiceDiscoveryAgent {
	multiclient := &multiClient{
		clients: clients,
	}
	return multiclient
}

func (m *multiClient) RegisterService(name, ipAddress, port, healthCheckPort string, checkFunction func() (bool, error), isDockerType bool, tags []string, metadata map[string]string) (string, error) {
	// not implemented as returning multiple service ids violates interface
	// to decide whether it is needed
	return "", nil
}

func (m *multiClient) DeregisterService(serviceID string) {
	for _, client := range m.clients {
		client.DeregisterService(serviceID)
	}
}

// GetHealthyServices returns service instances from all clients
func (m *multiClient) GetHealthyService(moduleName string) ([]string, error) {
	var endpoints []string
	for _, client := range m.clients {
		ep, err := client.GetHealthyService(moduleName)
		if err == nil {
			endpoints = append(endpoints, ep...)
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no instances found for %s", moduleName)
	}
	return endpoints, nil
}

// GetHealthyServiceWithZoneInfo returns service instances from all clients along with zone info
func (m *multiClient) GetHealthyServiceWithZoneInfo(moduleName string) ([]EndpointsWithExtraInfo, error) {
	var endpoints []EndpointsWithExtraInfo
	for _, client := range m.clients {
		ep, err := client.GetHealthyServiceWithZoneInfo(moduleName)
		if err == nil {
			endpoints = append(endpoints, ep...)
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no instances found for %s", moduleName)
	}
	return endpoints, nil
}
