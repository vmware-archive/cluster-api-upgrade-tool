package upgrade_test

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type log struct {}
func (l *log) Info(msg string, keysAndValues ...interface{}) {}
func (l *log) Enabled() bool { return false }
func (l *log) Error(err error, msg string, keysAndValues ...interface{}) {}
func (l *log) V(level int) logr.InfoLogger { return l}
func (l *log) WithValues(keysAndValues ...interface{}) logr.Logger { return l}
func (l *log) WithName(name string) logr.Logger { return l}

// config map namespacer
type cmn struct {
	ncmpc ncmpc
}
// NamespacedConfigMapClient can be private and we can redefine it here or public and reference it.
func (c *cmn) ConfigMaps(string) upgrade.NamespacedConfigMapClient {
	return &c.ncmpc
}

// namespaced config map client
type ncmpc struct {
	cm v1.ConfigMap
}
func (n *ncmpc)	Get(string, metav1.GetOptions) (*v1.ConfigMap, error) {
	return &n.cm, nil
}
func (n *ncmpc) Create(cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	return cm, nil
}

// pod namespacer
type pn struct {
	npc npc
}
func (p *pn) Pods(string) upgrade.NamespacedPodsClient {
	return &p.npc
}

// namespaced pod client
type npc struct {
	podList v1.PodList
}
func (n *npc) Get(string, metav1.GetOptions) (*v1.Pod, error) {
	return &n.podList.Items[0], nil
}
func (n *npc) List(options metav1.ListOptions) (*v1.PodList, error) {
	return &n.podList, nil
}

type nodeClient struct {
	nodeList *v1.NodeList
}
func (n *nodeClient) 	List(options metav1.ListOptions) (*v1.NodeList, error){
	return n.nodeList, nil
}

func TestNewTestableControlPlaneUpgrader(t *testing.T) {
	cpu := upgrade.NewTestableControlPlaneUpgrader(&log{}, &cmn{}, &pn{}, &nodeClient{})
	if err := cpu.Upgrade(); err != nil {
		t.Fatal(err)
	}
}
