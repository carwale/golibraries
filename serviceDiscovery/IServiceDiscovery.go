package serviceDiscovery

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
