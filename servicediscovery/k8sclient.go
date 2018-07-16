package servicediscovery

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type k8sClient struct {
	client         *kubernetes.Clientset
	isInK8sCluster bool
}

type K8SOptions func(k *k8sClient)

//IsInK8SCluster sets the IP for consul agent. Defults to 127.0.0.1
func IsInK8SCluster(flag bool) K8SOptions {
	return func(k *k8sClient) {
		k.isInK8sCluster = flag
	}
}

func NewK8sClient(options ...K8SOptions) IServiceDiscoveryAgent {

	client := &k8sClient{
		isInK8sCluster: true,
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

func (k *k8sClient) RegisterService(name, ipAddress, port string, checkFunction func() (bool, error)) (string, error) {
	return "", nil
}

func (k *k8sClient) DeregisterService(serviceID string) {

}

// GetHealthyServicesFromK8sCluster returns service instances from k8s cluster
func (k *k8sClient) GetHealthyService(moduleName string) ([]string, error) {

	endpoints, err := k.client.CoreV1().Endpoints("default").Get(moduleName, metav1.GetOptions{})

	if err != nil {
		return nil, err
	}

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

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
