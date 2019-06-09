package capkactuators

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vmware/cluster-api-upgrade-tool/pkg/execer"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/kind/nodes"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	capierror "sigs.k8s.io/cluster-api/pkg/controller/error"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/config"
	"sigs.k8s.io/kind/pkg/cluster/config/defaults"
	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/cluster/create"
	kindnodes "sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/container/cri"
	"sigs.k8s.io/kind/pkg/exec"
)

const Token = "abcdef.0123456789abcdef"

type Machine struct {
	Copy   *execer.Client
	Docker *execer.Client
	Stdout io.Writer
	Stderr io.Writer

	KubeconfigsDir string
}

func NewMachineActuator(kubeconfigs string) *Machine {
	return &Machine{
		Copy:           execer.NewClient("cp"),
		Docker:         execer.NewClient("docker"),
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		KubeconfigsDir: kubeconfigs,
	}
}

func (m *Machine) Create(ctx context.Context, c *clusterv1.Cluster, machine *clusterv1.Machine) error {
	fmt.Printf("Creating a machine for cluster %q\n", c.Name)
	clusterExists, err := cluster.IsKnown(c.Name)
	if err != nil {
		fmt.Printf("%+v", err)
		return err
	}
	fmt.Printf("Is there a cluster? %v\n", clusterExists)

	setValue := getRole(machine)

	fmt.Printf("This node has a role of %q\n", setValue)
	if setValue == constants.ControlPlaneNodeRoleValue {
		if clusterExists {
			fmt.Println("Does not yet support joining a control plane.")
			return nil
			//fmt.Println("Creating a new control plane node")
			//controlPlane, err := m.createNode(c.Name, constants.ControlPlaneNodeRoleValue)
			//if err != nil {
			//	fmt.Printf("%+v", err)
			//	return err
			//}
			//return nil
		}

		fmt.Println("Creating a brand new cluster")
		// TODO create external load balancer with first control plane
		return m.createCluster(c.Name)
	}

	// If no cluster exists yet, let's requeue this machine until a cluster does exist
	if !clusterExists {
		fmt.Printf("Sending machine %q back since there is no cluster to join\n", machine.Name)
		return &capierror.RequeueAfterError{RequeueAfter: 30 * time.Second}
	}

	fmt.Println("Creating a new worker node")
	// join a machine
	worker, err := m.createNode(c.Name, constants.WorkerNodeRoleValue)
	if err != nil {
		fmt.Printf("%+v", err)
		return err
	}
	if err := fixNode(worker); err != nil {
		fmt.Printf("%+v", err)
		return err
	}
	if err := joinWorker(worker, c.Name); err != nil {
		fmt.Println("%+v", err)
		return err
	}
	return nil
}
func (m *Machine) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	return nil
}
func (m *Machine) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	return nil
}
func (m *Machine) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) (bool, error) {
	fmt.Println("Looking for a docker container named", getKindName(machine))
	role := getRole(machine)
	nodeList, err := nodes.List(fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, role),
		fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, cluster.Name),
		fmt.Sprintf("name=%s", getKindName(machine)))
	if err != nil {
		return true, err
	}
	return len(nodeList) >= 1, nil
}

func getKindName(machine *clusterv1.Machine) string {
	annotations := machine.GetAnnotations()
	return annotations["name"]
}

func getRole(machine *clusterv1.Machine) string {
	// Figure out what kind of node we're making
	annotations := machine.GetAnnotations()
	setValue, ok := annotations["set"]
	if !ok {
		setValue = constants.WorkerNodeRoleValue
	}
	return setValue
}

func (m *Machine) createCluster(clusterName string) error {
	ctx := cluster.NewContext(clusterName)
	if err := ctx.Create(&config.Cluster{}, create.SetupKubernetes(true)); err != nil {
		return err
	}

	if err := m.Copy.RunCommand(ctx.KubeConfigPath(), m.KubeconfigsDir); err != nil {
		return err
	}
	return nil
}

func (m *Machine) createNode(clusterName, role string) (*nodes.Node, error) {
	clusterLabel := fmt.Sprintf("%s=%s", constants.ClusterLabelKey, clusterName)
	existingNodes, err := nodes.List(
		fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, role),
		fmt.Sprintf("label=%s", clusterLabel))
	if err != nil {
		return nil, err
	}

	mounts := []cri.Mount{}

	n := getName(clusterName, role, len(existingNodes))
	switch role {
	case constants.ExternalLoadBalancerNodeRoleValue:
		return nodes.CreateExternalLoadBalancerNode(n, defaults.Image, clusterLabel, "127.0.0.1", int32(0))
	case constants.ControlPlaneNodeRoleValue:
		return nodes.CreateControlPlaneNode(n, defaults.Image, clusterLabel, "127.0.0.1", int32(0), mounts)
	case constants.WorkerNodeRoleValue:
		return nodes.CreateWorkerNode(n, defaults.Image, clusterLabel, mounts)
	default:
		return nil, errors.New("unknown node role")
	}
}

func getName(clusterName, role string, count int) string {
	suffix := fmt.Sprintf("%d", count)
	if count == 0 {
		suffix = ""
	}
	return fmt.Sprintf("%s-%s%s", clusterName, role, suffix)
}

func fixNode(node *nodes.Node) error {
	fmt.Println("Fixing mounts")
	if err := node.FixMounts(); err != nil {
		return err
	}
	if kindnodes.NeedProxy() {
		fmt.Println("Setting proxy")
		if err := node.SetProxy(); err != nil {
			return err
		}
	}
	fmt.Println("Signaling start")
	if err := node.SignalStart(); err != nil {
		return err
	}
	fmt.Println("don't need to wait for docker!")
	return nil
}

func getJoinAddress(allNodes []nodes.Node) (string, error) {
	// get the control plane endpoint, in case the cluster has an external load balancer in
	// front of the control-plane nodes
	fmt.Println("getting control plane endpoint")
	controlPlaneEndpoint, err := nodes.GetControlPlaneEndpoint(allNodes)
	if err != nil {
		return "", err
	}
	fmt.Println("got control plane endpoint")
	// if the control plane endpoint is defined we are using it as a join address
	if controlPlaneEndpoint != "" {
		return controlPlaneEndpoint, nil
	}

	// otherwise, get the BootStrapControlPlane node
	controlPlaneHandle, err := nodes.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return "", err
	}
	fmt.Println("got bootstrap control plane node")
	// get the IP of the bootstrap control plane node
	controlPlaneIP, err := controlPlaneHandle.IP()
	if err != nil {
		return "", err
	}
	fmt.Println("got control plane ip")
	return fmt.Sprintf("%s:%d", controlPlaneIP, 6443), nil
}

func joinWorker(node *nodes.Node, clusterName string) error {
	fmt.Println("Listing nodes")
	allNodes, err := nodes.List(fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName))
	if err != nil {
		return nil
	}
	// get the join address
	joinAddress, err := getJoinAddress(allNodes)
	fmt.Println("Join Address:", joinAddress)
	if err != nil {
		// TODO(bentheelder): logging here
		return err
	}

	// run kubeadm join
	cmd := node.Command(
		"kubeadm", "join",
		// the join command uses the docker ip and a well know port that
		// are accessible only inside the docker network
		joinAddress,
		// uses a well known token and skipping ca certification for automating TLS bootstrap process
		"--token", Token,
		"--discovery-token-unsafe-skip-ca-verification",
		// preflight errors are expected, in particular for swap being enabled
		// TODO(bentheelder): limit the set of acceptable errors
		"--ignore-preflight-errors=all",
		// increase verbosity for debugging
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	for _, line := range lines {
		fmt.Println(line)
	}
	if err != nil {
		return err
	}
	return nil
}

type Cluster struct{}

func NewClusterActuator() *Cluster {
	return &Cluster{}
}

func (c *Cluster) Reconcile(cluster *clusterv1.Cluster) error {
	fmt.Printf("The cluster named %q has been created! Kind has nothing to do.\n", cluster.Name)
	return nil
}
func (c *Cluster) Delete(cluster *clusterv1.Cluster) error {
	fmt.Printf("The cluster named %q has been deleted. It's a no-op for capk!\n", cluster.Name)
	return nil
}
