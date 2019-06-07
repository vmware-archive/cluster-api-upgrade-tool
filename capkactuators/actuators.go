package capkactuators

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	capierror "sigs.k8s.io/cluster-api/pkg/controller/error"

	"github.com/vmware/cluster-api-upgrade-tool/pkg/execer"

	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/config"
	"sigs.k8s.io/kind/pkg/cluster/config/defaults"
	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/cluster/create"
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
	clusterExists, err := cluster.IsKnown(c.Name)
	if err != nil {
		fmt.Printf("%+v", err)
		return err
	}

	// Figure out what kind of node we're making
	annotations := c.GetAnnotations()
	setValue, ok := annotations["set"]
	if !ok {
		setValue = "worker"
	}

	if setValue == "controlPlane" {
		if clusterExists {
			if err := m.createNode(c.Name, constants.ControlPlaneNodeRoleValue); err != nil {
				fmt.Printf("%+v", err)
				return err
			}
			return nil
		}

		// TODO: kubeadm join --control-plane if the cluster exists
		return m.createCluster(c.Name)
	}

	// If no cluster exists yet, let's requeue this machine until a cluster does exist
	if !clusterExists {
		return &capierror.RequeueAfterError{RequeueAfter: 30 * time.Second}
	}

	// join a machine
	if err := m.createNode(c.Name, constants.WorkerNodeRoleValue); err != nil {
		fmt.Printf("%+v", err)
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
	return true, nil
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

// Create a node, but not the first node....
func (m *Machine) createNode(clusterName, role string) error {
	clusterLabel := fmt.Sprintf("%s=%s", constants.ClusterLabelKey, clusterName)
	existingNodes, err := nodes.List(
		fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, role),
		fmt.Sprintf("label=%s", clusterLabel))
	if err != nil {
		return err
	}

	n := m.name(clusterName, role, len(existingNodes))
	switch role {
	case constants.ControlPlaneNodeRoleValue:
		_, err := nodes.CreateControlPlaneNode(n, defaults.Image, clusterLabel, "127.0.0.1", int32(0), nil)
		return err
	case constants.WorkerNodeRoleValue:
		_, err = nodes.CreateWorkerNode(n, defaults.Image, clusterLabel, nil)
		return err
	default:
		return errors.New("unknown node role")
	}
}

func (m *Machine) name(clusterName, role string, count int) string {
	suffix := fmt.Sprintf("%d", count)
	if count == 0 {
		suffix = ""
	}
	return fmt.Sprintf("%s-%s%s", clusterName, role, suffix)
}

type Cluster struct{}

func NewClusterActuator() *Cluster {
	return &Cluster{}
}

func (c *Cluster) Reconcile(cluster *clusterv1.Cluster) error {
	fmt.Printf("The cluster named %q has been created! Kind has nothing to do.")
	return nil
}
func (c *Cluster) Delete(cluster *clusterv1.Cluster) error {
	fmt.Printf("The cluster named %q has been deleted. It's a no-op for capk!\n")
	return nil
}
