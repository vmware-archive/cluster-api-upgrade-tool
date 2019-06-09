package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	// This only exists because haproxy is internal to kind v0.2.1
	"github.com/vmware/cluster-api-upgrade-tool/pkg/execer"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/haproxy"

	"github.com/pkg/errors"
	"k8s.io/client-go/util/homedir"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cluster/config"
	"sigs.k8s.io/kind/pkg/cluster/config/defaults"
	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/cluster/create"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/container/cri"
	"sigs.k8s.io/kind/pkg/container/docker"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/fs"
)

const Token = "abcdef.0123456789abcdef"

func main() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Here we go!")
	clusterName := "my-cluster"
	for {
		// read input
		text, _ := reader.ReadString('\n')
		cleanText := strings.TrimSpace(text)
		inputs := strings.Split(cleanText, " ")
		switch inputs[0] {
		case "new-cluster":
			fmt.Println("Creating new cluster")
			if err := createCluster(clusterName); err != nil {
				panic(err)
			}
			ns, err := nodes.List(
				fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, constants.ControlPlaneNodeRoleValue),
				fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName),
				fmt.Sprintf("name=%s", getName(clusterName, constants.ControlPlaneNodeRoleValue, 2)),
			)
			if err != nil {
				panic(err)
			}
			fmt.Println("Hacking this by deleting the second CP until it's necessary")
			if err := nodes.Delete(ns...); err != nil {
				panic(err)
			}
		case "add-worker":
			fmt.Println("Adding a new worker")
			node, err := createNode(clusterName, constants.WorkerNodeRoleValue)
			if err != nil {
				panic(err)
			}
			if err := fixNode(node); err != nil {
				panic(err)
			}
			fmt.Printf("Created node: %s\n", node.Name())
			if err := joinWorker(node, clusterName); err != nil {
				panic(err)
			}
			fmt.Println("Node joined!")
		case "delete-worker":
			ns, err := nodes.List(
				fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, constants.WorkerNodeRoleValue),
				fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName),
				fmt.Sprintf("name=%s%s", getName(clusterName, "worker", 0), inputs[1]))
			if err != nil {
				panic(err)
			}
			if err := nodes.Delete(ns...); err != nil {
				panic(err)
			}
			fmt.Println("Deleted a node")
		case "add-control-plane":
			fmt.Println("Adding a control plane")
			node, err := createNode(clusterName, constants.ControlPlaneNodeRoleValue)
			if err != nil {
				panic(err)
			}
			if err := fixNode(node); err != nil {
				panic(err)
			}
			if err := joinControlPlane(node, clusterName); err != nil {
				fmt.Printf("%+v\n", err)
				panic(err)
			}
			fmt.Println("Created node: %s\n", node.Name())
		case "set-cluster-name":
			fmt.Println("setting cluster name...")
			clusterName = inputs[1]
		default:
			fmt.Println("Unknown command")
		}
		fmt.Println("Done!")
	}
}

func loadBalancerExists(clusterName string) (bool, error) {
	lb, err := nodes.List(
		fmt.Sprintf("label=%s=%s", constants.NodeRoleKey, constants.ExternalLoadBalancerNodeRoleValue),
		fmt.Sprintf("label=%s", clusterName))
	if err != nil {
		return false, err
	}
	return len(lb) >= 1, nil
}

func configureLoadBalancer(clusterName string) error {
	allNodes, err := nodes.List(fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName))
	if err != nil {
		return err
	}

	// identify external load balancer node
	loadBalancerNode, err := nodes.ExternalLoadBalancerNode(allNodes)
	if err != nil {
		return err
	}

	// otherwise notify the user
	fmt.Println("Configuring the load balancer️")

	// collect info about the existing controlplane nodes
	var backendServers = map[string]string{}
	controlPlaneNodes, err := nodes.SelectNodesByRole(
		allNodes,
		constants.ControlPlaneNodeRoleValue,
	)
	if err != nil {
		return err
	}
	for _, n := range controlPlaneNodes {
		controlPlaneIP, err := n.IP()
		if err != nil {
			return errors.Wrapf(err, "failed to get IP for node %s", n.Name())
		}
		backendServers[n.Name()] = fmt.Sprintf("%s:%d", controlPlaneIP, 6443)
	}

	// create haproxy config data
	haproxyConfig, err := haproxy.Config(&haproxy.ConfigData{
		ControlPlanePort: haproxy.ControlPlanePort,
		BackendServers:   backendServers,
	})
	if err != nil {
		return err
	}

	// create haproxy config on the node
	if err := loadBalancerNode.WriteFile("/kind/haproxy.cfg", haproxyConfig); err != nil {
		// TODO: logging here
		return errors.Wrap(err, "failed to write haproxy")
	}

	// starts a docker container with HA proxy load balancer
	if err := docker.Kill("SIGHUP", loadBalancerNode.Name()); err != nil {
		return errors.Wrap(err, "failed to reload loadbalancer")
	}

	return nil
}

// fixNode needs to run before kubeadm joins. It does things like start systemd.
func fixNode(node *nodes.Node) error {
	fmt.Println("Fixing mounts")
	if err := node.FixMounts(); err != nil {
		return err
	}
	if nodes.NeedProxy() {
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

func createCluster(clusterName string) error {
	copier := execer.NewClient("cp")
	ctx := cluster.NewContext(clusterName)
	if err := ctx.Create(&config.Cluster{
		Nodes: []config.Node{
			{
				Role: config.ControlPlaneRole,
			},
			{
				Role: config.ControlPlaneRole,
			},
		},
	}, create.SetupKubernetes(true)); err != nil {
		return err
	}

	if err := copier.RunCommand(ctx.KubeConfigPath(), "/kubeconfigs"); err != nil {
		return err
	}
	return nil
}

// Creates a node
func createNode(clusterName, role string) (*nodes.Node, error) {
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
	// load balancer case is broken in v0.2.1
	case constants.ExternalLoadBalancerNodeRoleValue:
		return nodes.CreateExternalLoadBalancerNode(n, haproxy.Image, clusterLabel, "127.0.0.1", int32(0))
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

// GetControlPlaneEndpoint returns the control plane endpoint in case the
// cluster has an external load balancer in front of the control-plane nodes,
// otherwise return an empty string.
func GetControlPlaneEndpoint(allNodes []nodes.Node) (string, error) {
	n, err := nodes.ExternalLoadBalancerNode(allNodes)
	if err != nil {
		return "", err
	}
	// if there is no external load balancer
	if n == nil {
		return "", nil
	}
	// gets the IP of the load balancer
	loadBalancerIP, err := n.IP()
	if err != nil {
		return "", errors.Wrapf(err, "failed to get IP for node: %s", n.Name())
	}
	ports, err := docker.Inspect(n.Name(), `{{with index .NetworkSettings.Ports "6443/tcp"}}{{with index . 0}}{{index . "HostPort"}}{{end}}{{end}}`)
	if err != nil {
		return "", errors.Wrap(err, "failed to get host port")
	}
	fmt.Println("ports")
	return fmt.Sprintf("%s:%s", loadBalancerIP, ports[0]), nil
}

func getJoinAddress(allNodes []nodes.Node) (string, error) {
	// get the control plane endpoint, in case the cluster has an external load balancer in
	// front of the control-plane nodes
	fmt.Println("getting control plane endpoint")
	controlPlaneEndpoint, err := GetControlPlaneEndpoint(allNodes)
	if err != nil {
		return "", err
	}
	fmt.Println("got control plane endpoint", controlPlaneEndpoint)
	// if the control plane endpoint is defined we are using it as a join address
	if controlPlaneEndpoint != "" {
		return controlPlaneEndpoint, nil
	}

	// TODO don't need this since we will always have a control plane endpoint
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
	fmt.Println("kubeadm join output:")
	fmt.Println(lines)
	if err != nil {
		return err
	}

	return nil
}
func joinControlPlane(node *nodes.Node, clusterName string) error {
	allNodes, err := nodes.List(fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName))
	if err != nil {
		return nil
	}
	joinAddress, err := getJoinAddress(allNodes)
	if err != nil {
		return err
	}

	// creates the folder tree for pre-loading necessary cluster certificates on the joining node
	if err := node.Command("mkdir", "-p", "/etc/kubernetes/pki/etcd").Run(); err != nil {
		return err
	}

	// define the list of necessary cluster certificates
	fileNames := []string{
		"ca.crt", "ca.key",
		"front-proxy-ca.crt", "front-proxy-ca.key",
		"sa.pub", "sa.key",
		"etcd/ca.crt", "etcd/ca.key",
	}

	// creates a temporary folder on the host that should acts as a transit area for moving necessary cluster
	// certificates
	tmpDir, err := fs.TempDir("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	err = os.MkdirAll(filepath.Join(tmpDir, "/etcd"), os.ModePerm)
	if err != nil {
		return err
	}

	// get the handle for the bootstrap control plane node (the source for necessary cluster certificates)
	controlPlaneHandle, err := nodes.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return err
	}

	// copies certificates from the bootstrap control plane node to the joining node
	for _, fileName := range fileNames {
		// sets the path of the certificate into a node
		containerPath := path.Join("/etc/kubernetes/pki", fileName)
		// set the path of the certificate into the tmp area on the host
		tmpPath := filepath.Join(tmpDir, fileName)
		// copies from bootstrap control plane node to tmp area
		if err := controlPlaneHandle.CopyFrom(containerPath, tmpPath); err != nil {
			return errors.Wrapf(err, "failed to copy certificate %s", fileName)
		}
		// copies from tmp area to joining node
		fmt.Printf("Copying %s to container %s\n", tmpPath, containerPath)
		if err := node.CopyTo(tmpPath, containerPath); err != nil {
			return errors.Wrapf(err, "failed to copy certificate %s", fileName)
		}
	}

	cmd := node.Command(
		"kubeadm", "join",
		// the join command uses the docker ip and a well know port that
		// are accessible only inside the docker network
		joinAddress,
		// set the node to join as control-plane
		"--experimental-control-plane",
		// uses a well known token and skips ca certification for automating TLS bootstrap process
		"--token", Token,
		"--discovery-token-unsafe-skip-ca-verification",
		// preflight errors are expected, in particular for swap being enabled
		// TODO(bentheelder): limit the set of acceptable errors
		"--ignore-preflight-errors=all",
		// increase verbosity for debug
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	fmt.Println("kubeadm join --experimental-control-plane output:")
	for _, line := range lines {
		fmt.Println(line)
	}
	if err != nil {
		return errors.Wrap(err, "failed to join a control plane node with kubeadm")
	}

	return nil
}

/** This is modified from the original version, nodes.createNode, since it's broken on v0.2.1 for load balancers **/
func createLoadBalancer(clusterName, name, clusterLabel, listenAddress string, port int32) error {
	if port == 0 {
		p, err := getPort()
		if err != nil {
			return err
		}
		port = p
	}

	runArgs := []string{
		"-d", // run the container detached
		// running containers in a container requires privileged
		// NOTE: we could try to replicate this with --cap-add, and use less
		// privileges, but this flag also changes some mounts that are necessary
		// including some ones docker would otherwise do by default.
		// for now this is what we want. in the future we may revisit this.
		"--privileged",
		"--security-opt", "seccomp=unconfined", // also ignore seccomp
		"--tmpfs", "/tmp", // various things depend on working /tmp
		"--tmpfs", "/run", // systemd wants a writable /run
		// some k8s things want /lib/modules
		"-v", "/lib/modules:/lib/modules:ro",
		"--hostname", name, // make hostname match container name
		"--name", name, // ... and set the container name
		// label the node with the cluster ID
		"--label", clusterLabel,
		// label the node with the role ID
		"--label", fmt.Sprintf("%s=%s", constants.NodeRoleKey, constants.ExternalLoadBalancerNodeRoleValue),
		// explicitly set the entrypoint
		"--expose", fmt.Sprintf("%d", port),
		"-p", fmt.Sprintf("%s:%d:%d", listenAddress, port, haproxy.ControlPlanePort),
	}

	// pass proxy environment variables to be used by node's docker deamon
	if nodes.NeedProxy() {
		proxyDetails := getProxyDetails()
		for key, val := range proxyDetails.Envs {
			runArgs = append(runArgs, "-e", fmt.Sprintf("%s=%s", key, val))
		}
	}

	if docker.UsernsRemap() {
		// We need this argument in order to make this command work
		// in systems that have userns-remap enabled on the docker daemon
		runArgs = append(runArgs, "--userns=host")
	}

	if _, err := docker.Run(
		haproxy.Image,
		docker.WithRunArgs(runArgs...),
	); err != nil {
		return errors.Wrap(err, "failed to run docker")
	}

	return nil
}

// Copying out private methods from kind...
// helper used to get a free TCP port for the API server
func getPort() (int32, error) {
	dummyListener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer dummyListener.Close()
	port := dummyListener.Addr().(*net.TCPAddr).Port
	return int32(port), nil
}

// Copying out private methods from kind
// getProxyDetails returns a struct with the host environment proxy settings
// that should be passed to the nodes
func getProxyDetails() proxyDetails {
	var proxyEnvs = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}
	var val string
	var details proxyDetails
	details.Envs = make(map[string]string)

	for _, name := range proxyEnvs {
		val = os.Getenv(name)
		if val != "" {
			details.Envs[name] = val
		} else {
			val = os.Getenv(strings.ToLower(name))
			if val != "" {
				details.Envs[name] = val
			}
		}
	}
	return details
}

// Copying out private struct from kind
// proxyDetails contains proxy settings discovered on the host
type proxyDetails struct {
	Envs map[string]string
	// future proxy details here
}

func kubeadmInit(clusterName string) error {
	allNodes, err := nodes.List(fmt.Sprintf("label=%s=%s", constants.ClusterLabelKey, clusterName))
	if err != nil {
		return nil
	}

	// get the target node for this task
	node, err := nodes.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return err
	}

	// run kubeadm
	cmd := node.Command(
		// init because this is the control plane node
		"kubeadm", "init",
		// preflight errors are expected, in particular for swap being enabled
		// TODO(bentheelder): limit the set of acceptable errors
		"--ignore-preflight-errors=all",
		// specify our generated config file
		"--config=/kind/kubeadm.conf",
		"--skip-token-print",
		// increase verbosity for debugging
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	for _, line := range lines {
		fmt.Println(line)
	}
	if err != nil {
		return errors.Wrap(err, "failed to init node with kubeadm")
	}

	// copies the kubeconfig files locally in order to make the cluster
	// usable with kubectl.
	// the kubeconfig file created by kubeadm internally to the node
	// must be modified in order to use the random host port reserved
	// for the API server and exposed by the node

	// retrives the random host where the API server is exposed
	// TODO(fabrizio pandini): when external load-balancer will be
	//      implemented this should be modified accordingly
	hostPort, err := node.Ports(6443)
	if err != nil {
		return errors.Wrap(err, "failed to get kubeconfig from node")
	}

	kubeConfigPath := KubeConfigPath(clusterName)
	if err := node.WriteKubeConfig(kubeConfigPath, hostPort); err != nil {
		return errors.Wrap(err, "failed to get kubeconfig from node")
	}

	// install the CNI network plugin
	// TODO(bentheelder): this should possibly be a different action?
	// TODO(bentheelder): support other overlay networks
	// first probe for a pre-installed manifest
	haveDefaultCNIManifest := true
	if err := node.Command("test", "-f", "/kind/manifests/default-cni.yaml").Run(); err != nil {
		haveDefaultCNIManifest = false
	}
	if haveDefaultCNIManifest {
		// we found the default manifest, install that
		// the images should already be loaded along with kubernetes
		if err := node.Command(
			"kubectl", "create", "--kubeconfig=/etc/kubernetes/admin.conf",
			"-f", "/kind/manifests/default-cni.yaml",
		).Run(); err != nil {
			return errors.Wrap(err, "failed to apply overlay network")
		}
	} else {
		// fallback to our old pattern of installing weave using their recommended method
		if err := node.Command(
			"/bin/sh", "-c",
			`kubectl apply --kubeconfig=/etc/kubernetes/admin.conf -f "https://cloud.weave.works/k8s/net?k8s-version=$(kubectl version --kubeconfig=/etc/kubernetes/admin.conf | base64 | tr -d '\n')"`,
		).Run(); err != nil {
			return errors.Wrap(err, "failed to apply overlay network")
		}
	}

	// if we are only provisioning one node, remove the master taint
	// https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/#master-isolation
	if len(allNodes) == 1 {
		if err := node.Command(
			"kubectl", "--kubeconfig=/etc/kubernetes/admin.conf",
			"taint", "nodes", "--all", "node-role.kubernetes.io/master-",
		).Run(); err != nil {
			return errors.Wrap(err, "failed to remove master taint")
		}
	}

	// add the default storage class
	// TODO(bentheelder): this should possibly be a different action?
	if err := addDefaultStorageClass(node); err != nil {
		return errors.Wrap(err, "failed to add default storage class")
	}

	return nil
}

// a default storage class
// we need this for e2es (StatefulSet)
const defaultStorageClassManifest = `# host-path based default storage class
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  namespace: kube-system
  name: standard
  annotations:
    storageclass.beta.kubernetes.io/is-default-class: "true"
  labels:
    addonmanager.kubernetes.io/mode: EnsureExists
provisioner: kubernetes.io/host-path`

func addDefaultStorageClass(controlPlane *nodes.Node) error {
	in := strings.NewReader(defaultStorageClassManifest)
	cmd := controlPlane.Command(
		"kubectl",
		"--kubeconfig=/etc/kubernetes/admin.conf", "apply", "-f", "-",
	)
	cmd.SetStdin(in)
	return cmd.Run()
}

func KubeConfigPath(clusterName string) string {
	// configDir matches the standard directory expected by kubectl etc
	configDir := filepath.Join(homedir.HomeDir(), ".kube")
	// note that the file name however does not, we do not want to overwrite
	// the standard config, though in the future we may (?) merge them
	fileName := fmt.Sprintf("kind-config-%s", clusterName)
	return filepath.Join(configDir, fileName)
}
