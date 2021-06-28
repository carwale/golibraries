package servicediscovery

//IServiceDiscoveryAgent is the interface that every service discovery agent
//should implement

type EndpointsWithExtraInfo struct {
	Address string
	Zone    string
}

type IServiceDiscoveryAgent interface {
	//RegisterService will register the service given the name, ip and port
	//It returns the ID of the service
	RegisterService(name, ipAddress, port, healthCheckPort string, checkFunction func() (bool, error), isDockerType bool) (string, error)
	//DeregisterService will deregister the service given the ID
	DeregisterService(serviceID string)
	//GetHealthyService will give a list of all the instances of the module
	GetHealthyService(moduleName string) ([]string, error)
	//GetHealthyServiceWithZoneInfo will give a list of all the instances of the module along with other infor like zones for all the pods
	GetHealthyServiceWithZoneInfo(moduleName string) ([]EndpointsWithExtraInfo, error)
}
