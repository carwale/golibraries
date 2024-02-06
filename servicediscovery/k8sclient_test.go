package servicediscovery

import (
	"os"
	"testing"
)

var k8sCli IServiceDiscoveryAgent

// run the test by running go test --kubeconfig=<location of kubeconfig>
func TestMain(m *testing.M) {

	k8sCli = NewK8sClient(SetK8sNamespace("dev"), IsInK8SCluster(false))

	code := m.Run()
	os.Exit(code)
}

func TestEndpointSlices(t *testing.T) {

	endpoints, err := k8sCli.GetHealthyServiceWithZoneInfo("bhrigu-prod", "production")

	if err != nil {
		println(err.Error())
	}

	for _, endpoint := range endpoints {
		println(endpoint.Address + " " + endpoint.Zone)
	}
}
