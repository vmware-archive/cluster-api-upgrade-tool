// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/internal/kubernetes"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

const (
	etcdCACertFile = "/etc/kubernetes/pki/etcd/ca.crt"
	etcdCertFile   = "/etc/kubernetes/pki/etcd/peer.crt"
	etcdKeyFile    = "/etc/kubernetes/pki/etcd/peer.key"
)

type ControlPlaneUpgrader struct {
	*base
	oldNodeToEtcdMember map[string]string
}

func NewControlPlaneUpgrader(log logr.Logger, config Config) (*ControlPlaneUpgrader, error) {
	b, err := newBase(log, config)
	if err != nil {
		return nil, errors.Wrap(err, "error initializing upgrader")
	}

	return &ControlPlaneUpgrader{
		base: b,
	}, nil
}

// Upgrade does the upgrading of the control plane.
func (u *ControlPlaneUpgrader) Upgrade() error {
	machines, err := u.listMachines()
	if err != nil {
		return err
	}

	if machines == nil || len(machines.Items) == 0 {
		return errors.New("Found 0 control plane machines")
	}

	min, max, err := u.minMaxControlPlaneVersions(machines)
	if err != nil {
		return errors.Wrap(err, "error determining current control plane versions")
	}

	// default the desired version if the user did not specify it
	if unsetVersion.EQ(u.userVersion) {
		u.desiredVersion = max
	}

	if isMinorVersionUpgrade(min, u.desiredVersion) {
		err = u.updateKubeletConfigMapIfNeeded(u.desiredVersion)
		if err != nil {
			return err
		}

		err = u.updateKubeletRbacIfNeeded(u.desiredVersion)
		if err != nil {
			return err
		}
	}

	if err := u.etcdClusterHealthCheck(time.Minute * 1); err != nil {
		return err
	}

	if err := u.UpdateProviderIDsToNodes(); err != nil {
		return err
	}

	return u.updateMachines(machines)
}

func isMinorVersionUpgrade(base, update semver.Version) bool {
	return base.Major == update.Major && base.Minor < update.Minor
}

func (u *ControlPlaneUpgrader) minMaxControlPlaneVersions(machines *clusterapiv1alpha1.MachineList) (semver.Version, semver.Version, error) {
	var min, max semver.Version

	for _, machine := range machines.Items {
		if machine.Spec.Versions.ControlPlane != "" {
			machineVersion, err := semver.ParseTolerant(machine.Spec.Versions.ControlPlane)
			if err != nil {
				return min, max, errors.Wrapf(err, "invalid control plane version %q for machine %s/%s", machine.Spec.Versions.ControlPlane, machine.Namespace, machine.Name)
			}
			if min.EQ(unsetVersion) || machineVersion.LT(min) {
				min = machineVersion
			}
			if max.EQ(unsetVersion) || machineVersion.GT(max) {
				max = machineVersion
			}
		}
	}

	return min, max, nil
}

func (u *ControlPlaneUpgrader) updateKubeletConfigMapIfNeeded(version semver.Version) error {
	// Check if the desired configmap already exists
	desiredKubeletConfigMapName := fmt.Sprintf("kubelet-config-%d-%d", version.Major, version.Minor)
	_, err := u.targetKubernetesClient.CoreV1().ConfigMaps("kube-system").Get(desiredKubeletConfigMapName, metav1.GetOptions{})
	if err == nil {
		u.log.Info("kubelet configmap already exists", "configMapName", desiredKubeletConfigMapName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "error determining if configmap %s exists", desiredKubeletConfigMapName)
	}

	// If we get here, we have to make the configmap
	previousMinorVersionKubeletConfigMapName := fmt.Sprintf("kubelet-config-%d-%d", version.Major, version.Minor-1)
	cm, err := u.targetKubernetesClient.CoreV1().ConfigMaps("kube-system").Get(previousMinorVersionKubeletConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return errors.Errorf("unable to find current kubelet configmap %s", previousMinorVersionKubeletConfigMapName)
	}
	cm.Name = desiredKubeletConfigMapName
	cm.ResourceVersion = ""

	_, err = u.targetKubernetesClient.CoreV1().ConfigMaps("kube-system").Create(cm)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.Wrapf(err, "error creating configmap %s", desiredKubeletConfigMapName)
	}

	return nil
}

func (u *ControlPlaneUpgrader) updateKubeletRbacIfNeeded(version semver.Version) error {
	majorMinor := fmt.Sprintf("%d.%d", version.Major, version.Minor)
	roleName := fmt.Sprintf("kubeadm:kubelet-config-%s", majorMinor)

	_, err := u.targetKubernetesClient.RbacV1().Roles("kube-system").Get(roleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		newRole := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      roleName,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         []string{"get"},
					APIGroups:     []string{""},
					Resources:     []string{"configmaps"},
					ResourceNames: []string{fmt.Sprintf("kubelet-config-%s", majorMinor)},
				},
			},
		}

		_, err := u.targetKubernetesClient.RbacV1().Roles("kube-system").Create(newRole)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return errors.Wrapf(err, "error creating role %s", roleName)
		}
	} else if err != nil {
		return errors.Wrapf(err, "error determining if role %s exists", roleName)
	}

	_, err = u.targetKubernetesClient.RbacV1().RoleBindings("kube-system").Get(roleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		newRoleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
				Name:      roleName,
			},
			Subjects: []rbacv1.Subject{
				{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Group",
					Name:     "system:nodes",
				},
				{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Group",
					Name:     "system:bootstrappers:kubeadm:default-node-token",
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     roleName,
			},
		}

		_, err = u.targetKubernetesClient.RbacV1().RoleBindings("kube-system").Create(newRoleBinding)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
	} else if err != nil {
		return errors.Wrapf(err, "error determining if rolebinding %s exists", roleName)
	}

	return nil
}

func (u *ControlPlaneUpgrader) addMachine(source *clusterapiv1alpha1.Machine) (*clusterapiv1alpha1.Machine, error) {
	newMachine := source.DeepCopy()

	// have to clear this out so we can create a new machine
	newMachine.ResourceVersion = ""

	// have to clear this out so the new machine can get its own provider id set
	newMachine.Spec.ProviderID = nil

	// assume the original name is controlplane-<index> or controlplane-<index>-<timestamp>
	// let's set the new name to controlplane-<index>-<timestamp>
	nameParts := strings.Split(source.Name, "-")
	if len(nameParts) < 2 {
		return nil, errors.Errorf("machine name %q does not match expected format <name>-<index> or <name>-<index>-<timestamp>", source.Name)
	}
	newMachine.Name = fmt.Sprintf("%s-%s-%d", nameParts[0], nameParts[1], time.Now().Unix())

	newMachine.Spec.Versions.ControlPlane = u.desiredVersion.String()
	newMachine.Spec.Versions.Kubelet = u.desiredVersion.String()

	u.log.Info("Creating new machine", "name", newMachine)

	createdMachine, err := u.managementClusterAPIClient.Machines(u.clusterNamespace).Create(newMachine)
	if err != nil {
		return nil, errors.Wrapf(err, "Error creating machine: %s", newMachine.Name)
	}

	return createdMachine, nil
}

func (u *ControlPlaneUpgrader) etcdClusterHealthCheck(timeout time.Duration) error {
	u.log.Info("etcdClusterHealthCheck")
	// get pods in kube-system with label component=etcd
	pods, err := u.targetKubernetesClient.CoreV1().Pods("kube-system").List(metav1.ListOptions{LabelSelector: "component=etcd"})
	if err != nil {
		return errors.Wrap(err, "error listing etcd pods")
	}
	if len(pods.Items) < 1 {
		return errors.New("found 0 etcd pods")
	}

	firstEtcdPodName := pods.Items[0].Name
	endpoint := fmt.Sprintf("https://%s:2379", pods.Items[0].Status.PodIP)

	// get the first one
	opts := kubernetes.PodExecInput{
		RestConfig:       u.targetRestConfig,
		KubernetesClient: u.targetKubernetesClient,
		Namespace:        "kube-system",
		Name:             firstEtcdPodName,
		Command: []string{
			"etcdctl",
			"--ca-file", etcdCACertFile,
			"--cert-file", etcdCertFile,
			"--key-file", etcdKeyFile,
			"--endpoints", endpoint,
			"cluster-health",
		},
		Timeout: timeout,
	}

	stdout, stderr, err := kubernetes.PodExec(opts)

	// @TODO figure out how we want logs to show up in this library
	u.log.Info(fmt.Sprintf("etcdClusterHealthCheck stdout: %s", stdout))
	u.log.Info(fmt.Sprintf("etcdClusterHealthCheck stderr: %s", stderr))

	return err
}

func (u *ControlPlaneUpgrader) updateMachines(machines *clusterapiv1alpha1.MachineList) error {
	// save all etcd member id corresponding to node before upgrade starts
	err := u.oldNodeToEtcdMemberId(time.Minute * 1)
	if err != nil {
		return err
	}

	// TODO add more error logs on failure conditions
	for _, machine := range machines.Items {
		if machine.Spec.ProviderID == nil {
			u.log.Info("unable to upgrade machine as it has no spec.providerID", "name", machine.Name)
			continue
		}

		originalProviderID, err := kubernetes.ParseProviderID(*machine.Spec.ProviderID)
		if err != nil {
			return err
		}

		oldNode := u.GetNodeFromProviderID(originalProviderID)
		oldHostName := hostnameForNode(oldNode)

		// 1. create new machine
		createdMachine, err := u.addMachine(&machine)
		if err != nil {
			return err
		}

		// 2. wait for it to have a provider id
		newProviderID, err := u.waitForMachineProviderID(createdMachine.Namespace, createdMachine.Name, 15*time.Minute)
		if err != nil {
			return err
		}
		p, err := kubernetes.ParseProviderID(newProviderID)
		if err != nil {
			u.log.Error(err, "error parsing new provider id", "provider-id", newProviderID)
		} else {
			newProviderID = p
		}

		// 3. wait for the new node to show up with the matching provider id
		newNode, err := u.waitForNodeWithProviderID(newProviderID, 5*time.Minute)
		if err != nil {
			u.log.Error(err, "Failed to find a new node with provider id", "provider-id", newProviderID)
			return err
		}

		// 4. Wait for node to be ready
		nodeHostname := hostnameForNode(newNode)
		if nodeHostname == "" {
			u.log.Info("unable to find hostname for node", "node", newNode.Name)
			return errors.Errorf("unable to find hostname for node %s", newNode.Name)
		}
		err = wait.PollImmediate(15*time.Second, 15*time.Minute, func() (bool, error) {
			ready := u.componentHealthCheck(nodeHostname)
			return ready, nil
		})
		if err != nil {
			return errors.Wrapf(err, "components on node %s are not ready", newNode.Name)
		}

		// delete old etcd member
		err = u.deleteEtcdMember(time.Minute*1, nodeHostname, u.oldNodeToEtcdMember[oldHostName])
		if err != nil {
			return errors.Wrapf(err, "unable to delete old etcd member %s", u.oldNodeToEtcdMember[oldHostName])
		}

		if err := u.deleteMachine(&machine); err != nil {
			return err
		}
	}
	return nil
}

// retry the given function for the given number of times with the given interval
func (u *ControlPlaneUpgrader) retry(node *v1.Node, count int, interval time.Duration, fn func(hp *v1.Node) error) error {
	if err := fn(node); err != nil {
		if count--; count > 0 {
			time.Sleep(interval)
			return u.retry(node, count, interval, fn)
		}

		return err
	}

	return nil
}

func (u *ControlPlaneUpgrader) waitForNodeWithProviderID(providerID string, timeout time.Duration) (*v1.Node, error) {
	u.log.Info("Waiting for node", "provider-id", providerID)
	var node *v1.Node

	err := wait.PollImmediate(5*time.Second, timeout, func() (bool, error) {
		if err := u.UpdateProviderIDsToNodes(); err != nil {
			return false, err
		}

		n := u.GetNodeFromProviderID(providerID)
		if n != nil {
			node = n
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return nil, errors.Wrap(err, "timed out waiting for node provider id")
	}

	u.log.Info("Found node", "name", node.Name)
	return node, nil
}

func (u *ControlPlaneUpgrader) waitForMachineProviderID(machineNamespace, machineName string, timeout time.Duration) (string, error) {
	u.log.Info("waitForMachineProviderID start", "namespace", machineNamespace, "name", machineName)
	var providerID string

	err := wait.PollImmediate(5*time.Second, timeout, func() (bool, error) {
		machine, err := u.managementClusterAPIClient.Machines(machineNamespace).Get(machineName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if machine.Spec.ProviderID == nil {
			return false, nil
		}

		providerID = *machine.Spec.ProviderID
		return providerID != "", nil
	})

	if err != nil {
		return "", errors.Wrap(err, "timed out waiting for machine provider id")
	}

	u.log.Info("Got providerID", "provider-id", providerID)
	return providerID, nil
}

func (u *ControlPlaneUpgrader) deleteMachine(machine *clusterapiv1alpha1.Machine) error {
	u.log.Info("Deleting existing machine", "namespace", machine.Namespace, "name", machine.Name)

	propagationPolicy := metav1.DeletePropagationForeground
	deleteOptions := &metav1.DeleteOptions{
		PropagationPolicy: &propagationPolicy,
	}

	return u.managementClusterAPIClient.Machines(u.clusterNamespace).Delete(machine.Name, deleteOptions)
}

func hostnameForNode(node *v1.Node) string {
	for _, address := range node.Status.Addresses {
		if address.Type == v1.NodeHostName {
			return address.Address
		}
	}
	return ""
}

// componentHealthCheck checks that the etcd, apiserver, scheduler, and controller manager static pods are healthy.
func (u *ControlPlaneUpgrader) componentHealthCheck(nodeHostname string) bool {
	u.log.Info("Component health check for node", "hostname", nodeHostname)

	components := []string{"etcd", "kube-apiserver", "kube-scheduler", "kube-controller-manager"}
	requiredConditions := sets.NewString("PodScheduled", "Initialized", "Ready", "ContainersReady")

	for _, component := range components {
		foundConditions := sets.NewString()

		podName := fmt.Sprintf("%s-%v", component, nodeHostname)
		log := u.log.WithValues("pod", podName)

		log.Info("Getting pod")
		pod, err := u.targetKubernetesClient.CoreV1().Pods("kube-system").Get(podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			log.Info("Pod not found yet")
			return false
		} else if err != nil {
			log.Error(err, "error getting pod")
			return false
		}

		for _, condition := range pod.Status.Conditions {
			if condition.Status == "True" {
				foundConditions.Insert(string(condition.Type))
			}
		}

		missingConditions := requiredConditions.Difference(foundConditions)
		if missingConditions.Len() > 0 {
			missingDescription := strings.Join(missingConditions.List(), ",")
			log.Info("pod is missing some required conditions", "conditions", missingDescription)
			return false
		}
	}

	return true
}

// Split this into getting machines
// Then pulling provider IDs
func (u *ControlPlaneUpgrader) listMachines() (*clusterapiv1alpha1.MachineList, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("cluster.k8s.io/cluster-name=%s,set=controlplane", u.clusterName),
	}

	u.log.Info("Listing machines", "labelSelector", listOptions.LabelSelector)
	machines, err := u.managementClusterAPIClient.Machines(u.clusterNamespace).List(listOptions)
	if err != nil {
		return nil, errors.Wrap(err, "error listing machines")
	}

	return machines, nil
}

func (u *ControlPlaneUpgrader) oldNodeToEtcdMemberId(timeout time.Duration) error {
	u.log.Info("oldNodeToEtcdMemberId")
	pods, err := u.targetKubernetesClient.CoreV1().Pods("kube-system").List(metav1.ListOptions{LabelSelector: "component=etcd"})
	if err != nil {
		return errors.Wrap(err, "error listing etcd pods")
	}
	if len(pods.Items) < 1 {
		return errors.New("found 0 etcd pods")
	}

	firstEtcdPodName := pods.Items[0].Name
	endpoint := fmt.Sprintf("https://%s:2379", pods.Items[0].Status.PodIP)

	opts := kubernetes.PodExecInput{
		RestConfig:       u.targetRestConfig,
		KubernetesClient: u.targetKubernetesClient,
		Namespace:        "kube-system",
		Name:             firstEtcdPodName,
		Command: []string{
			"etcdctl",
			"--ca-file", etcdCACertFile,
			"--cert-file", etcdCertFile,
			"--key-file", etcdKeyFile,
			"--endpoints", endpoint,
			"member",
			"list",
		},
		Timeout: timeout,
	}

	stdout, stderr, err := kubernetes.PodExec(opts)

	// @TODO figure out how we want logs to show up in this library
	u.log.Info(fmt.Sprintf("oldNodeToEtcdMemberId stdout: %s", stdout))
	u.log.Info(fmt.Sprintf("oldNodeToEtcdMemberId stderr: %s", stderr))

	if err != nil {
		return errors.Wrap(err, "no etcd member found")
	}

	lines := strings.Split(stdout, "\n")
	nodeIPtoEtcdMemberMap := make(map[string]string)

	for _, line := range lines {
		if len(line) > 0 {
			words := strings.Split(line, " ")

			nodeName := strings.Split(words[1], "=")
			node := nodeName[1]
			etcdMemberId := words[0][:len(words[0])-1]
			nodeIPtoEtcdMemberMap[node] = etcdMemberId
		}
	}

	u.oldNodeToEtcdMember = nodeIPtoEtcdMemberMap

	return nil
}

// deleteEtcdMember deletes the old etcd member
func (u *ControlPlaneUpgrader) deleteEtcdMember(timeout time.Duration, newNode string, etcdMemberId string) error {
	u.log.Info("deleteEtcdMember")
	pods, err := u.targetKubernetesClient.CoreV1().Pods("kube-system").List(metav1.ListOptions{LabelSelector: "component=etcd"})
	if err != nil {
		return errors.Wrap(err, "error listing etcd pods")
	}
	if len(pods.Items) < 1 {
		return errors.New("found 0 etcd pods")
	}

	var endpointIP string
	var etcdPodName string
	found := false
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == newNode {
			endpointIP = pod.Status.PodIP
			etcdPodName = pod.Name
			found = true
			break
		}
	}

	if !found {
		return errors.New("no new etcd pod found running on node" + newNode)
	}
	endpoint := fmt.Sprintf("https://%s:2379", endpointIP)

	opts := kubernetes.PodExecInput{
		RestConfig:       u.targetRestConfig,
		KubernetesClient: u.targetKubernetesClient,
		Namespace:        "kube-system",
		Name:             etcdPodName,
		Command: []string{
			"etcdctl",
			"--ca-file", etcdCACertFile,
			"--cert-file", etcdCertFile,
			"--key-file", etcdKeyFile,
			"--endpoints", endpoint,
			"member",
			"remove",
			etcdMemberId,
		},
		Timeout: timeout,
	}

	stdout, stderr, err := kubernetes.PodExec(opts)

	// @TODO figure out how we want logs to show up in this library
	u.log.Info(fmt.Sprintf("deleteEtcdMember stdout: %s", stdout))
	u.log.Info(fmt.Sprintf("deleteEtcdMember stderr: %s", stderr))

	if err != nil {
		return errors.Wrap(err, "no etcd member found")
	}

	return nil
}
