package capkactuators

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vmware/cluster-api-upgrade-tool/pkg/execer"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/kind/actions"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	capierror "sigs.k8s.io/cluster-api/pkg/controller/error"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
)

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
			fmt.Println("Adding a control plane")
			return actions.AddControlPlane(c.Name)
		}

		fmt.Println("Creating a brand new cluster")
		return actions.CreateControlPlane(c.Name)
	}

	controlPlanes, err := actions.ListControlPlanes(c.Name)
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
	// If there are no control plane then we should hold off on joining workers
	if len(controlPlanes) == 0 {
		fmt.Printf("Sending machine %q back since there is no cluster to join\n", machine.Name)
		return &capierror.RequeueAfterError{RequeueAfter: 30 * time.Second}
	}

	fmt.Println("Creating a new worker node")
	return actions.AddWorker(c.Name)
}
func (m *Machine) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	return actions.DeleteNode(cluster.Name, getKindName(machine))
}

func (m *Machine) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	fmt.Println("Not implemented yet.")
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

type Cluster struct{}

func NewClusterActuator() *Cluster {
	return &Cluster{}
}

func (c *Cluster) Reconcile(cluster *clusterv1.Cluster) error {
	fmt.Printf("The cluster named %q has been created! Setting up some infrastructure.\n", cluster.Name)
	return actions.SetUpLoadBalancer(cluster.Name)
}

func (c *Cluster) Delete(cluster *clusterv1.Cluster) error {
	fmt.Println("Not implemented.")
	return nil
}
