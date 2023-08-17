package servicediscovery

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type k8sClient struct {
	client         *kubernetes.Clientset
	isInK8sCluster bool
	namespace      string
}

type K8SOptions func(k *k8sClient)

//IsInK8SCluster sets whether running inside kubernetes cluster. Defults to true.
func IsInK8SCluster(flag bool) K8SOptions {
	return func(k *k8sClient) {
		k.isInK8sCluster = flag
	}
}

//SetK8sNamespace sets the namespace to be used for querying k8s. Defaults to 'default'
func SetK8sNamespace(namespace string) K8SOptions {
	return func(k *k8sClient) {
		k.namespace = namespace
	}
}

//NewK8sClient returns new K8s Service discovery agent
func NewK8sClient(options ...K8SOptions) IServiceDiscoveryAgent {

	client := &k8sClient{
		isInK8sCluster: true,
		namespace:      "default",
	}

	for _, option := range options {
		option(client)
	}
	var kubeconfig *string
	var err error
	var config *rest.Config

	if client.isInK8sCluster {
		config, err = rest.InClusterConfig()
	} else {
		// use the current context in kubeconfig
		if home := homeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	}

	if err != nil {
		panic(err.Error())
	}

	client.client, err = kubernetes.NewForConfig(config)

	if err != nil {
		panic(err.Error())
	}

	return client
}

func (k *k8sClient) RegisterService(name, ipAddress, port, healthCheckPort string, checkFunction func() (bool, error), isDockerType bool) (string, error) {
	return "", nil
}

func (k *k8sClient) DeregisterService(serviceID string) {

}

// GetHealthyServicesFromK8sCluster returns service instances from k8s cluster
func (k *k8sClient) GetHealthyService(moduleName string) ([]string, error) {

	endpoints, err := k.client.CoreV1().Endpoints(k.namespace).Get(context.Background(), moduleName, metav1.GetOptions{})

	if err != nil {
		return nil, err
	}
	fmt.Printf("Endpoints fetched: %v\n", endpoints)
	for _, subset := range endpoints.Subsets {
		if len(subset.Ports) > 0 {
			port := subset.Ports[0].Port
			instances := make([]string, len(subset.Addresses))
			for idx, address := range subset.Addresses {
				instances[idx] = address.IP + ":" + strconv.Itoa(int(port))
			}
			return instances, nil
		}
	}
	return nil, fmt.Errorf("No instances found for %s", moduleName)
}

// GetHealthyServiceWithZoneInfo returns all endpoints of a service along with zone info
func (k *k8sClient) GetHealthyServiceWithZoneInfo(moduleName string) ([]EndpointsWithExtraInfo, error) {

	endpointSlicesList, err:= k.client.DiscoveryV1().EndpointSlices(k.namespace).List(context.Background(), v1.ListOptions{LabelSelector: "kubernetes.io/service-name="+moduleName})
	if err != nil {
		return nil, err
	}
	fmt.Printf("Endpoints fetched: %v\n", endpointSlicesList)
	if len(endpointSlicesList.Items) > 0 {
		fmt.Printf("Endpoints fetched: %v\n", endpointSlicesList)
		var instances []EndpointsWithExtraInfo
		for _, endpointSlice := range endpointSlicesList.Items {

			if len(endpointSlice.Ports) > 0 {
				port := endpointSlice.Ports[0].Port

				for _, endpoint := range endpointSlice.Endpoints {
					if len(endpoint.Addresses) > 0 {
						if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
							for _, address := range endpoint.Addresses {
								instances = append(instances, EndpointsWithExtraInfo{
									Address: address + ":" + strconv.Itoa(int(*port)),
									Zone: *endpoint.Zone,
								})
							}
						}
					}
				}
			}
		}
		return instances, nil
	}
	return nil, fmt.Errorf("No instances found for %s", moduleName)
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
