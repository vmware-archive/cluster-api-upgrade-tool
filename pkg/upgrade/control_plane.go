// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	bootstrapv1 "sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm/api/v1alpha2"
	kubeadmv1beta1 "sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm/kubeadm/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/controllers/noderefutil"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kubernetes2 "github.com/vmware/cluster-api-upgrade-tool/pkg/internal/kubernetes"
)

const (
	etcdCACertFile       = "/etc/kubernetes/pki/etcd/ca.crt"
	etcdCertFile         = "/etc/kubernetes/pki/etcd/peer.crt"
	etcdKeyFile          = "/etc/kubernetes/pki/etcd/peer.key"
	kubeadmConfigMapName = "kubeadm-config"
	LabelNodeRoleMaster  = "node-role.kubernetes.io/master"

	// annotationPrefix is the prefix for all annotations managed by this tool.
	annotationPrefix = "upgrade.cluster-api.vmware.com/"

	// AnnotationUpgradeID is the annotation key for an upgrade's identifier.
	AnnotationUpgradeID = annotationPrefix + "id"

	AnnotationMachineNameBase = annotationPrefix + "machine-name-base"
)

type ControlPlaneUpgrader struct {
	log                     logr.Logger
	desiredVersion          semver.Version
	clusterNamespace        string
	clusterName             string
	managementClusterClient ctrlclient.Client
	targetRestConfig        *rest.Config
	targetKubernetesClient  kubernetes.Interface
	providerIDsToNodes      map[string]*v1.Node
	upgradeID               string
	oldNodeToEtcdMember     map[string]string
	secretsUpdated          bool
	infrastructurePatch     jsonpatch.Patch
	bootstrapPatch          jsonpatch.Patch
	kubeadmConfigMapPatch   jsonpatch.Patch
	machineTimeout          time.Duration
}

func NewControlPlaneUpgrader(log logr.Logger, config Config) (*ControlPlaneUpgrader, error) {
	// Validations
	if config.TargetCluster.Namespace == "" {
		return nil, errors.New("target cluster namespace is required")
	}
	if config.TargetCluster.Name == "" {
		return nil, errors.New("target cluster name is required")
	}
	if config.KubernetesVersion == "" {
		return nil, errors.New("kubernetes version is required")
	}
	if config.UpgradeID == "" {
		return nil, errors.New("upgrade ID is required")
	}
	// Kubernetes resource names must be DNS1123 subdomains. Because the upgrade ID becomes part of the name for new
	// machines, infra machines, and KubeadmConfigs, we use the same validation here.
	if errs := validation.IsDNS1123Subdomain(config.UpgradeID); len(errs) > 0 {
		return nil, errors.New("upgrade ID: " + strings.Join(errs, ", "))
	}
	if config.MachineTimeout.Duration == 0 {
		return nil, errors.New("machine timeout must be greater than 0")
	}

	parsedVersion, err := semver.ParseTolerant(config.KubernetesVersion)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing kubernetes version %q", config.KubernetesVersion)
	}

	managementClusterClient, err := kubernetes2.NewClient(
		kubernetes2.KubeConfigPath(config.ManagementCluster.Kubeconfig),
		kubernetes2.KubeConfigContext(config.ManagementCluster.Context),
	)
	if err != nil {
		return nil, err
	}

	log.Info("Retrieving cluster from management cluster", "cluster-namespace", config.TargetCluster.Namespace, "cluster-name", config.TargetCluster.Name)
	cluster := &clusterv1.Cluster{}
	err = managementClusterClient.Get(context.TODO(), ctrlclient.ObjectKey{Namespace: config.TargetCluster.Namespace, Name: config.TargetCluster.Name}, cluster)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	kc, err := kubeconfig.FromSecret(managementClusterClient, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving cluster kubeconfig secret")
	}
	targetRestConfig, err := clientcmd.RESTConfigFromKubeConfig(kc)
	if err != nil {
		return nil, err
	}
	if targetRestConfig == nil {
		return nil, errors.New("could not get a kubeconfig for your target cluster")
	}

	log.Info("Creating target kubernetes client")
	targetKubernetesClient, err := kubernetes.NewForConfig(targetRestConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating target cluster client")
	}

	infoMessage := fmt.Sprintf("Rerun with `--upgrade-id=%s` if this upgrade fails midway and you want to retry", config.UpgradeID)
	log.Info(infoMessage)

	var infrastructurePatch, bootstrapPatch, kubeadmConfigMapPatch jsonpatch.Patch
	if len(config.Patches.Infrastructure) > 0 {
		infrastructurePatch, err = jsonpatch.DecodePatch([]byte(config.Patches.Infrastructure))
		if err != nil {
			return nil, errors.Wrap(err, "error decoding infrastructure patch")
		}
	}
	if len(config.Patches.Bootstrap) > 0 {
		bootstrapPatch, err = jsonpatch.DecodePatch([]byte(config.Patches.Bootstrap))
		if err != nil {
			return nil, errors.Wrap(err, "error decoding bootstrap patch")
		}
	}
	if len(config.Patches.KubeadmConfigMap) > 0 {
		kubeadmConfigMapPatch, err = jsonpatch.DecodePatch([]byte(config.Patches.KubeadmConfigMap))
		if err != nil {
			return nil, errors.Wrap(err, "error decoding kubeadm configmap patch")
		}
	}

	log.Info(fmt.Sprintf("Running upgrade with following configuration: %+v", config))

	return &ControlPlaneUpgrader{
		log:                     log,
		desiredVersion:          parsedVersion,
		clusterNamespace:        config.TargetCluster.Namespace,
		clusterName:             config.TargetCluster.Name,
		managementClusterClient: managementClusterClient,
		targetRestConfig:        targetRestConfig,
		targetKubernetesClient:  targetKubernetesClient,
		upgradeID:               config.UpgradeID,
		infrastructurePatch:     infrastructurePatch,
		bootstrapPatch:          bootstrapPatch,
		kubeadmConfigMapPatch:   kubeadmConfigMapPatch,
		machineTimeout:          config.MachineTimeout.Duration,
	}, nil
}

// Upgrade does the upgrading of the control plane.
func (u *ControlPlaneUpgrader) Upgrade() error {
	machines, err := u.listMachines()
	if err != nil {
		return err
	}

	if len(machines) == 0 {
		return errors.New("Found 0 control plane machines")
	}
	u.log.Info("Found control plane machines", "count", len(machines))

	// Begin the upgrade by reconciling the kubeadm ConfigMap's ClusterStatus.APIEndpoints, just in case the data
	// is out of sync.
	if err := u.reconcileKubeadmConfigMapAPIEndpoints(); err != nil {
		return err
	}

	err = u.updateKubeletConfigMapIfNeeded(u.desiredVersion)
	if err != nil {
		return err
	}

	err = u.updateKubeletRbacIfNeeded(u.desiredVersion)
	if err != nil {
		return err
	}

	u.log.Info("Checking etcd health")
	if err := u.etcdClusterHealthCheck(15 * time.Second); err != nil {
		return err
	}

	u.log.Info("Updating provider IDs to nodes")
	if err := u.UpdateProviderIDsToNodes(); err != nil {
		return err
	}

	u.log.Info("Updating kubernetes version")
	if err := u.updateKubeadmConfigMap(func(in *v1.ConfigMap) (*v1.ConfigMap, error) {
		return updateKubeadmKubernetesVersion(in, "v"+u.desiredVersion.String())
	}); err != nil {
		return err
	}

	if u.kubeadmConfigMapPatch != nil {
		u.log.Info("Patching kubeadm ConfigMap")
		if err := u.updateKubeadmConfigMap(func(in *v1.ConfigMap) (*v1.ConfigMap, error) {

			var clusterConfig kubeadmv1beta1.ClusterConfiguration
			if err := yaml.Unmarshal([]byte(in.Data["ClusterConfiguration"]), &clusterConfig); err != nil {
				return nil, errors.Wrap(err, "error decoding kubeadm ClusterConfiguration")
			}

			patched, err := patchRuntimeObject(&clusterConfig, u.kubeadmConfigMapPatch)
			if err != nil {
				return nil, errors.Wrap(err, "error patching kubeadm ClusterConfiguration")
			}

			b, err := yaml.Marshal(patched)
			if err != nil {
				return nil, errors.Wrap(err, "error marshaling patched kubeadm ClusterConfiguration")
			}

			cm := in.DeepCopy()
			cm.Data["ClusterConfiguration"] = string(b)

			return cm, nil
		}); err != nil {
			return err
		}
	}

	// save all etcd member id corresponding to node before upgrade starts
	if err := u.oldNodeToEtcdMemberId(time.Minute * 1); err != nil {
		return err
	}

	// reconcile machine annotation in the beginning for all the machines as
	// part of this upgrade process.
	for _, m := range machines {
		if err := u.reconcileMachineAnnotations(m); err != nil {
			u.log.Error(err, "Unable to reconcile machine annotations",
				"machine",
				fmt.Sprintf("%s/%s", m.Namespace, m.Name),
			)
			return err
		}
	}

	// handle all replacement machines first in case they are in an unhealthy
	// state.
	u.log.Info("Reconciling replacement machines")
	if err := u.reconcileReplacements(machines); err != nil {
		return err
	}

	u.log.Info("Updating machines")
	if err := u.updateMachines(machines); err != nil {
		return err
	}

	u.log.Info("Removing upgrade annotations")
	for _, m := range machines {
		var replacement clusterv1.Machine
		replacementName := generateReplacementMachineName(m.Annotations[AnnotationMachineNameBase], u.upgradeID)

		key := ctrlclient.ObjectKey{
			Namespace: m.Namespace,
			Name:      replacementName,
		}

		if err := u.managementClusterClient.Get(context.TODO(), key, &replacement); apierrors.IsNotFound(err) {
			// if there is a bad replacement machine in the beginning of the
			// upgrade it is eventually deleted but the reference of the
			// machine object is still in the slice of machines so just log
			// this error and continue.
			u.log.Info("skipping annotation removal from machine as it no longer exists", "machine", key.String())
			continue
		} else if err != nil {
			return errors.Wrapf(err, "error getting machine %s", key.String())
		}

		helper, err := patch.NewHelper(replacement.DeepCopy(), u.managementClusterClient)
		if err != nil {
			return err
		}

		delete(replacement.Annotations, AnnotationUpgradeID)

		if err := helper.Patch(context.TODO(), &replacement); err != nil {
			return err
		}
	}

	if err := u.reconcileKubeadmConfigMapAPIEndpoints(); err != nil {
		return err
	}

	return nil
}

func (u *ControlPlaneUpgrader) reconcileKubeadmConfigMapAPIEndpoints() error {
	u.log.Info("Listing workload cluster Nodes")
	nodeList, err := u.targetKubernetesClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing workload cluster nodes")
	}

	u.log.Info("Reconciling kubeadm ConfigMap's ClusterStatus.APIEndpoints")
	if err := u.updateKubeadmConfigMap(func(in *v1.ConfigMap) (*v1.ConfigMap, error) {
		return reconcileKubeadmConfigMapClusterStatusAPIEndpoints(in, nodeList)
	}); err != nil {
		return errors.Wrap(err, "error reconciling kubeadm ConfigMap")
	}

	return nil
}

func (u *ControlPlaneUpgrader) updateKubeletConfigMapIfNeeded(version semver.Version) error {
	// Check if the desired configmap already exists
	desiredKubeletConfigMapName := fmt.Sprintf("kubelet-config-%d.%d", version.Major, version.Minor)

	log := u.log.WithValues("name", desiredKubeletConfigMapName)

	log.Info("Checking for existence of kubelet configmap")
	_, err := u.targetKubernetesClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(desiredKubeletConfigMapName, metav1.GetOptions{})
	if err == nil {
		log.Info("kubelet configmap already exists")
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "error determining if kubelet configmap %s exists", desiredKubeletConfigMapName)
	}

	// If we get here, we have to make the configmap
	log.Info("Need to create kubelet configmap")

	previousMinorVersionKubeletConfigMapName := fmt.Sprintf("kubelet-config-%d.%d", version.Major, version.Minor-1)

	log.Info("Retrieving kubelet configmap for previous minor version", "previous-name", previousMinorVersionKubeletConfigMapName)
	cm, err := u.targetKubernetesClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(previousMinorVersionKubeletConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return errors.Errorf("unable to find kubelet configmap %s", previousMinorVersionKubeletConfigMapName)
	}

	cm.Name = desiredKubeletConfigMapName
	cm.ResourceVersion = ""

	log.Info("Creating kubelet configmap as a copy from the previous minor version")
	_, err = u.targetKubernetesClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Create(cm)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.Wrapf(err, "error creating configmap %s", desiredKubeletConfigMapName)
	}
	log.Info("kubelet configmap creation succeeded")

	return nil
}

func (u *ControlPlaneUpgrader) updateKubeletRbacIfNeeded(version semver.Version) error {
	majorMinor := fmt.Sprintf("%d.%d", version.Major, version.Minor)
	roleName := fmt.Sprintf("kubeadm:kubelet-config-%s", majorMinor)

	log := u.log.WithValues("role-name", "kube-system/"+roleName)

	log.Info("Looking up role")
	_, err := u.targetKubernetesClient.RbacV1().Roles(metav1.NamespaceSystem).Get(roleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		newRole := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: metav1.NamespaceSystem,
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

		log.Info("Need to create role")
		_, err := u.targetKubernetesClient.RbacV1().Roles(metav1.NamespaceSystem).Create(newRole)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return errors.Wrapf(err, "error creating role %s", roleName)
		}
		log.Info("Role creation succeeded")
	} else if err != nil {
		return errors.Wrapf(err, "error determining if role %s exists", roleName)
	}

	log.Info("Looking up role binding")
	_, err = u.targetKubernetesClient.RbacV1().RoleBindings(metav1.NamespaceSystem).Get(roleName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		newRoleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: metav1.NamespaceSystem,
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

		log.Info("Need to create role binding")
		_, err = u.targetKubernetesClient.RbacV1().RoleBindings(metav1.NamespaceSystem).Create(newRoleBinding)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return errors.Wrapf(err, "error creating rolebinding %s", roleName)
		}
		log.Info("Role binding creation succeeded")
	} else if err != nil {
		return errors.Wrapf(err, "error determining if rolebinding %s exists", roleName)
	}

	return nil
}

func (u *ControlPlaneUpgrader) etcdClusterHealthCheck(timeout time.Duration) error {
	members, err := u.listEtcdMembers(timeout)
	if err != nil {
		return err
	}

	var endpoints []string
	for _, member := range members {
		endpoints = append(endpoints, member.ClientURLs...)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	// TODO: we can switch back to using --cluster instead of --endpoints when we no longer need to support etcd 3.2
	// (which is the version kubeadm installs for Kubernetes v1.13.x). kubeadm switched to etcd 3.3 with v1.14.x.

	// TODO: use '-w json' when it's in the minimum supported etcd version.
	_, _, err = u.etcdctl(ctx, "endpoint health --endpoints", strings.Join(endpoints, ","))
	return err
}

func (u *ControlPlaneUpgrader) updateMachine(replacementKey ctrlclient.ObjectKey, machine *clusterv1.Machine, replacementBootstrap *bootstrapv1.KubeadmConfig) error {
	log := u.log.WithValues(
		"machine", fmt.Sprintf("%s/%s", machine.Namespace, machine.Name),
		"replacement", replacementKey.String(),
	)

	log.Info("Checking if we need to create a new machine")
	replacementRef := v1.ObjectReference{
		APIVersion: clusterv1.GroupVersion.String(),
		Kind:       "Machine",
		Namespace:  replacementKey.Namespace,
		Name:       replacementKey.Name,
	}
	obj, err := u.resourceExists(replacementRef)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return errors.WithStack(err)
		}
	}

	var replacementMachine *clusterv1.Machine
	if obj == nil {
		log.Info("New machine does not exist - need to create a new one")
		replacementMachine = machine.DeepCopy()

		// have to clear this out so we can create a new machine
		replacementMachine.ResourceVersion = ""

		// have to clear this out so the new machine can get its own provider id set
		replacementMachine.Spec.ProviderID = nil

		// Use the new, generated replacement machine name for all the things
		replacementMachine.Name = replacementKey.Name
		replacementMachine.Spec.InfrastructureRef.Name = replacementKey.Name
		replacementMachine.Spec.Bootstrap.Data = nil
		replacementMachine.Spec.Bootstrap.ConfigRef.Name = replacementKey.Name

		desiredVersion := u.desiredVersion.String()
		replacementMachine.Spec.Version = &desiredVersion

		log.Info("Creating new machine")
		if err := u.managementClusterClient.Create(context.TODO(), replacementMachine); err != nil {
			return errors.Wrapf(err, "Error creating machine: %s", replacementMachine.Name)
		}
		log.Info("Create succeeded")
	} else {
		log.Info("New machine exists - retrieving from server")
		replacementMachine = new(clusterv1.Machine)
		if err := u.managementClusterClient.Get(context.TODO(), replacementKey, replacementMachine); err != nil {
			return errors.Wrapf(err, "error getting replacement machine %s", replacementKey.String())
		}
	}

	if err := u.isHealthy(replacementMachine); err != nil {
		return err
	}

	// Update secrets once the replacement has been made healthy so that secrets
	// aren't potentially garbage collected when recovering from an incomplete
	// upgrade. We update _just before_ removing the old machine so that there is
	// no window where the secrets are owned by a non-existent machine.
	if err := u.updateSecrets(replacementBootstrap); err != nil {
		return err
	}

	// This used to happen when a new machine was created as a side effect. Must still update the mapping.
	if err := u.UpdateProviderIDsToNodes(); err != nil {
		return err
	}

	if err := u.removeMachine(machine); err != nil {
		return err
	}

	return nil
}

func (u *ControlPlaneUpgrader) reconcileMachineAnnotations(machine *clusterv1.Machine) error {
	log := u.log.WithValues(
		"machine", fmt.Sprintf("%s/%s", machine.Namespace, machine.Name),
		"upgrade-id", u.upgradeID,
	)
	if machine.Annotations == nil {
		machine.Annotations = make(map[string]string)
	}

	// If upgrade ID is already present, return early
	if machine.Annotations[AnnotationUpgradeID] != "" {
		return nil
	}

	helper, err := patch.NewHelper(machine.DeepCopy(), u.managementClusterClient)
	if err != nil {
		return errors.Wrap(err, "error creating patch helper for machine annotations")
	}

	log.Info("Adding upgrade ID annotation")
	machine.Annotations[AnnotationUpgradeID] = u.upgradeID

	// Only add if it's not present
	if _, found := machine.Annotations[AnnotationMachineNameBase]; !found {
		log.Info("Adding machine name base annotation")
		machine.Annotations[AnnotationMachineNameBase] = machine.Name
	}

	log.Info("Patching machine annotations")
	if err := helper.Patch(context.TODO(), machine); err != nil {
		return errors.Wrap(err, "error patching machine annotations")
	}

	return nil
}

func (u *ControlPlaneUpgrader) isReplacementMachine(machine *clusterv1.Machine) bool {
	return machine.Name == generateReplacementMachineName(machine.Annotations[AnnotationMachineNameBase], u.upgradeID)
}

func (u *ControlPlaneUpgrader) shouldSkipMachine(log logr.Logger, machine *clusterv1.Machine) bool {
	if machine.Spec.ProviderID == nil {
		log.Info("Unable to upgrade machine as it has no spec.providerID")
		return true
	}

	// Don't process a mismatching upgrade ID
	if machine.Annotations[AnnotationUpgradeID] != u.upgradeID {
		// TODO record that we're unable to upgrade because the ID is a mismatch (annotation? event?)
		log.Info("Unable to upgrade machine - mismatching upgrade id", "machine-upgrade-id", machine.Annotations[AnnotationUpgradeID])
		return true
	}

	// Skip if this machine doesn't have NameBase annotation
	baseName := machine.Annotations[AnnotationMachineNameBase]
	if baseName == "" {
		// should never happen because we call reconcileMachineAnnotations for all
		// machines in the beginning.
		log.Info("machine is missing annotation " + AnnotationMachineNameBase)
		return true
	}

	// Skip if this is a replacement machine for the current upgrade
	if u.isReplacementMachine(machine) {
		log.Info("Skipping machine as it is a replacement machine for the in-process upgrade")
		return true
	}

	if machine.Spec.Bootstrap.ConfigRef == nil {
		log.Info("Skipping machine because spec.bootstrap.configRef is nil")
		return true
	}

	bootstrapConfigRefGroupVersion, err := schema.ParseGroupVersion(machine.Spec.Bootstrap.ConfigRef.APIVersion)
	if err != nil {
		// should never happen
		log.Error(err, "error parsing spec.bootstrap.configRef.apiVersion", "value", machine.Spec.Bootstrap.ConfigRef.APIVersion)
		return true
	}

	if bootstrapConfigRefGroupVersion.Group != bootstrapv1.GroupVersion.Group || machine.Spec.Bootstrap.ConfigRef.Kind != "KubeadmConfig" {
		log.Info("Skipping machine because spec.bootstrap.configRef is not for a KubeadmConfig",
			"apiVersion", machine.Spec.Bootstrap.ConfigRef.APIVersion,
			"kind", machine.Spec.Bootstrap.ConfigRef.Kind,
		)
		return true
	}

	return false
}

// isHealthy verifies if a machine is healthy.
// Conditions for health:
// - the machine should have a ProviderID
// - the machine should have a node associated with the ProviderID
// - the node should be Ready
// - the node should have the LabelNodeRoleMaster
func (u *ControlPlaneUpgrader) isHealthy(machine *clusterv1.Machine) error {
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, u.machineTimeout)
	defer cancel()

	newProviderID, err := u.waitForMachineWithProviderID(ctx, machine.Namespace, machine.Name)
	if err != nil {
		return err
	}
	node, err := u.waitForNodeWithProviderID(ctx, newProviderID)
	if err != nil {
		return err
	}
	if err := u.waitForNodeReady(ctx, node); err != nil {
		return err
	}
	if err := u.waitForNodeWithMasterLabel(ctx, node); err != nil {
		return err
	}

	return nil
}

// removeMachine removes the machine and its references.
// It deletes and waits for the machine to be deleted.
// If the machine has a ProviderID, it proceeds to remove the corresponding
// etcd member and entry from the KubeadmConfigMap.
func (u *ControlPlaneUpgrader) removeMachine(machine *clusterv1.Machine) error {
	ctx := context.TODO()
	ctx, cancel := context.WithTimeout(ctx, u.machineTimeout)
	defer cancel()

	log := u.log.WithValues(
		"machine", fmt.Sprintf("%s/%s", machine.Namespace, machine.Name),
		"upgrade", u.upgradeID,
	)
	const deleteMachineInterval = 10 * time.Second
	var (
		err  error
		node *v1.Node
	)

	// Delete the machine from etcd member list if there is a ProviderID for
	// the machine. A replacement machine could've been created without a
	// ProviderID assigned.
	if machine.Spec.ProviderID != nil {
		machineProviderID, err := noderefutil.NewProviderID(*machine.Spec.ProviderID)
		if err != nil {
			return err
		}
		log.Info("Determined provider id for machine", "provider-id", machineProviderID.String())

		node = u.GetNodeFromProviderID(machineProviderID.ID())
		if node == nil {
			log.Info("Couldn't retrieve Node", "provider-id", machineProviderID.String())
			return fmt.Errorf("unknown previous node %q", machineProviderID.String())
		}
		// NOTE: this list is curated in the beginning. If the machine was a
		// replacement and it had a corresponding replacement node registered,
		// it would be in this list as well.
		oldEtcdMemberID := u.oldNodeToEtcdMember[node.Name]
		// Delete the etcd member, if necessary
		if oldEtcdMemberID != "" {
			// TODO make timeout the last arg, for consistency (or pass in a ctx?)
			err = u.deleteEtcdMember(time.Minute*1, oldEtcdMemberID)
			if err != nil {
				return errors.Wrapf(err, "unable to delete old etcd member %s", oldEtcdMemberID)
			}
		}
	}

	log.Info("Deleting machine")
	err = wait.PollImmediateUntil(deleteMachineInterval, func() (bool, error) {
		if err := u.managementClusterClient.Delete(ctx, machine); err != nil {
			log.Error(err, "error deleting machine")
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "timed out asking to delete machine %s/%s", machine.Namespace, machine.Name)
	}

	log.Info("Waiting for machine to be deleted")
	err = wait.PollImmediateUntil(deleteMachineInterval, func() (bool, error) {
		var tempMachine clusterv1.Machine
		key := ctrlclient.ObjectKey{
			Namespace: machine.Namespace,
			Name:      machine.Name,
		}
		if err := u.managementClusterClient.Get(ctx, key, &tempMachine); apierrors.IsNotFound(err) {
			log.Info("Machine has been deleted")
			return true, nil
		}
		return false, nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "timed out waiting for machine to be deleted %s/%s", machine.Namespace, machine.Name)
	}

	// Delete the node from the KubeadmConfigMap if there is one.
	if node != nil {
		// remove node from apiEndpoints in Kubeadm config map
		log.Info("Removing machine from kubeadm ConfigMap")
		err = wait.PollImmediateUntil(deleteMachineInterval, func() (bool, error) {
			if err := u.updateKubeadmConfigMap(func(in *v1.ConfigMap) (*v1.ConfigMap, error) {
				return removeNodeFromKubeadmConfigMapClusterStatusAPIEndpoints(in, node.Name)
			}); err != nil {

				log.Error(err, "error removing machine from kubeadm ConfigMap")
				return false, nil
			}
			return true, nil
		}, ctx.Done())
		if err != nil {
			return errors.Wrapf(err, "timed out removing machine %s/%s from kubeadm ConfigMap", machine.Namespace, machine.Name)
		}
	}

	return nil
}

func (u *ControlPlaneUpgrader) reconcileReplacements(machines []*clusterv1.Machine) error {
	var errs []error
	for _, m := range machines {
		if !u.isReplacementMachine(m) {
			continue
		}

		log := u.log.WithValues(
			"machine", fmt.Sprintf("%s/%s", m.Namespace, m.Name),
			"upgrade-id", u.upgradeID,
		)
		log.Info("Found replacement machine for the in-process upgrade")

		if err := u.isHealthy(m); err != nil {
			log.Error(err, "Replacement machine is not healthy, going to remove all references of it")
			if err := u.removeMachine(m); err != nil {
				errs = append(errs, err)
			}
			continue
		}

		log.Info("Replacement machine is healthy")
	}

	return kerrors.NewAggregate(errs)
}

func (u *ControlPlaneUpgrader) updateMachines(machines []*clusterv1.Machine) error {
	for _, machine := range machines {
		log := u.log.WithValues(
			"machine", fmt.Sprintf("%s/%s", machine.Namespace, machine.Name),
			"upgrade-id", u.upgradeID,
		)

		if u.shouldSkipMachine(log, machine) {
			continue
		}

		log.Info("Checking etcd health")
		err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
			if err := u.etcdClusterHealthCheck(15 * time.Second); err != nil {
				log.Error(err, "etcd health check failed - retrying")
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return errors.Wrap(err, "timed out waiting for etcd health check to pass")
		}

		replacementMachineName := generateReplacementMachineName(machine.Annotations[AnnotationMachineNameBase], u.upgradeID)
		replacementKey := ctrlclient.ObjectKey{
			Namespace: u.clusterNamespace,
			Name:      replacementMachineName,
		}

		log.Info("Updating infrastructure reference",
			"api-version", machine.Spec.InfrastructureRef.APIVersion,
			"kind", machine.Spec.InfrastructureRef.Kind,
			"name", machine.Spec.InfrastructureRef.Name,
		)
		if err := u.updateInfrastructureReference(replacementKey, machine.Spec.InfrastructureRef); err != nil {
			return err
		}

		log.Info("Updating bootstrap reference",
			"api-version", machine.Spec.Bootstrap.ConfigRef.APIVersion,
			"kind", machine.Spec.Bootstrap.ConfigRef.Kind,
			"name", machine.Spec.Bootstrap.ConfigRef.Name,
		)

		replacementBootstrap, err := u.updateBootstrapConfig(replacementKey, machine.Spec.Bootstrap.ConfigRef.Name)
		if err != nil {
			return err
		}

		log.Info("Updating machine")
		if err := u.updateMachine(replacementKey, machine, replacementBootstrap); err != nil {
			return err
		}
	}

	return nil
}

// generateReplacementMachineName takes the base machine name and appends the upgrade suffix to it. If the generated
// name would be longer than the maximum allowed name length, generateReplacementMachineName truncates the original name
// until the upgrade suffix fits.
func generateReplacementMachineName(base, upgradeID string) string {
	machineName := base
	machineSuffix := "-" + upgradeID

	excess := len(machineName) + len(machineSuffix) - validation.DNS1123SubdomainMaxLength
	if excess > 0 {
		max := len(machineName) - excess
		machineName = machineName[0:max]
	}

	return machineName + machineSuffix
}

// filterOutMachineOwners filters out owner references of kind "Machine",
// leaving owner references of other kinds. This allows the Machine controller
// to adopt the objects after they're created, but allows us to preserve owner
// refs of various other kinds
func filterOutMachineOwners(ownerRefs []metav1.OwnerReference) []metav1.OwnerReference {
	filtered := make([]metav1.OwnerReference, 0, len(ownerRefs))
	for _, owner := range ownerRefs {
		if owner.Kind == "Machine" {
			continue
		}
		filtered = append(filtered, owner)
	}

	return filtered
}

func (u *ControlPlaneUpgrader) updateBootstrapConfig(replacementKey ctrlclient.ObjectKey, configName string) (*bootstrapv1.KubeadmConfig, error) {
	// Step 1: return early if we've already created the replacement infra resource
	replacementRef := v1.ObjectReference{
		APIVersion: bootstrapv1.GroupVersion.String(),
		Kind:       "KubeadmConfig",
		Namespace:  replacementKey.Namespace,
		Name:       replacementKey.Name,
	}
	obj, err := u.resourceExists(replacementRef)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, errors.WithStack(err)
		}
	}

	if obj != nil {
		bootstrap := &bootstrapv1.KubeadmConfig{}
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), bootstrap)
		if err != nil {
			return nil, err
		}
		return bootstrap, nil
	}

	// Step 2: if we're here, we need to create it

	// copy node registration
	bootstrap := &bootstrapv1.KubeadmConfig{}
	bootstrapKey := ctrlclient.ObjectKey{
		Name:      configName,
		Namespace: u.clusterNamespace,
	}
	if err := u.managementClusterClient.Get(context.TODO(), bootstrapKey, bootstrap); err != nil {
		return nil, errors.WithStack(err)
	}

	// modify bootstrap config
	bootstrap.SetName(replacementKey.Name)
	bootstrap.SetResourceVersion("")

	ownerRefs := filterOutMachineOwners(bootstrap.GetOwnerReferences())
	bootstrap.SetOwnerReferences(ownerRefs)

	// find node registration
	nodeRegistration := kubeadmv1beta1.NodeRegistrationOptions{}
	if bootstrap.Spec.InitConfiguration != nil {
		nodeRegistration = bootstrap.Spec.InitConfiguration.NodeRegistration
	} else if bootstrap.Spec.JoinConfiguration != nil {
		nodeRegistration = bootstrap.Spec.JoinConfiguration.NodeRegistration
	}
	if bootstrap.Spec.JoinConfiguration == nil {
		bootstrap.Spec.JoinConfiguration = &kubeadmv1beta1.JoinConfiguration{
			ControlPlane: &kubeadmv1beta1.JoinControlPlane{},
		}
	}
	bootstrap.Spec.JoinConfiguration.NodeRegistration = nodeRegistration
	bootstrap.Spec.JoinConfiguration.Discovery.BootstrapToken = &kubeadmv1beta1.BootstrapTokenDiscovery{}

	// clear init configuration
	// When you have both the init configuration and the join configuration present
	// for a control plane upgrade, kubeadm will use the init configuration instead
	// of the join configuration. during upgrades, you will never be initializing a
	// new node. It will always be joining an existing control plane.
	bootstrap.Spec.InitConfiguration = nil

	// Convert to a runtime.Object in case we need to patch, so we don't have to type assert after patching
	var toCreate runtime.Object = bootstrap

	if u.bootstrapPatch != nil {
		toCreate, err = patchRuntimeObject(bootstrap, u.bootstrapPatch)
		if err != nil {
			return nil, errors.Wrap(err, "error patching bootstrap resource")
		}
	}

	err = u.managementClusterClient.Create(context.TODO(), toCreate)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	// Set bootstrap to the results of the Create call so generated values are
	// not lost
	bootstrap = toCreate.(*bootstrapv1.KubeadmConfig)

	return bootstrap, nil
}

func (u *ControlPlaneUpgrader) updateSecrets(bootstrap *bootstrapv1.KubeadmConfig) error {
	// Return early if we've already updated the ownerRefs
	if u.secretsUpdated {
		return nil
	}

	secretNames := []string{
		fmt.Sprintf("%s-ca", u.clusterName),
		fmt.Sprintf("%s-etcd", u.clusterName),
		fmt.Sprintf("%s-sa", u.clusterName),
		fmt.Sprintf("%s-proxy", u.clusterName),
	}

	for _, secretName := range secretNames {
		secret := &v1.Secret{}
		secretKey := ctrlclient.ObjectKey{Name: secretName, Namespace: u.clusterNamespace}
		if err := u.managementClusterClient.Get(context.TODO(), secretKey, secret); err != nil {
			return errors.WithStack(err)
		}
		helper, err := patch.NewHelper(secret.DeepCopy(), u.managementClusterClient)
		if err != nil {
			return err
		}

		secret.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: bootstrapv1.GroupVersion.String(),
				Kind:       "KubeadmConfig",
				Name:       bootstrap.Name,
				UID:        bootstrap.UID,
			},
		})

		if err := helper.Patch(context.TODO(), secret); err != nil {
			return err
		}
	}

	u.secretsUpdated = true

	return nil
}

func (u *ControlPlaneUpgrader) resourceExists(ref v1.ObjectReference) (*unstructured.Unstructured, error) {
	obj := new(unstructured.Unstructured)
	obj.SetAPIVersion(ref.APIVersion)
	obj.SetKind(ref.Kind)
	key := ctrlclient.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}
	if err := u.managementClusterClient.Get(context.TODO(), key, obj); err != nil {
		return nil, err
	}

	return obj, nil
}

func patchRuntimeObject(obj runtime.Object, patch jsonpatch.Patch) (runtime.Object, error) {
	j, err := json.Marshal(obj)
	if err != nil {
		return nil, errors.Wrap(err, "error converting to JSON")
	}

	patched, err := patch.Apply(j)
	if err != nil {
		return nil, errors.Wrap(err, "error applying patches")
	}

	// Create a new instance of the same type as obj
	t := reflect.TypeOf(obj)
	v := reflect.New(t.Elem()).Interface()

	// Unmarshal into that new instance
	if err := json.Unmarshal(patched, v); err != nil {
		return nil, errors.Wrap(err, "error converting from patched JSON")
	}

	return v.(runtime.Object), nil
}

func (u *ControlPlaneUpgrader) updateInfrastructureReference(replacementKey ctrlclient.ObjectKey, ref v1.ObjectReference) error {
	// Step 1: return early if we've already created the replacement infra resource
	replacementRef := v1.ObjectReference{
		APIVersion: ref.APIVersion,
		Kind:       ref.Kind,
		Namespace:  replacementKey.Namespace,
		Name:       replacementKey.Name,
	}
	obj, err := u.resourceExists(replacementRef)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return errors.WithStack(err)
		}
	}

	if obj != nil {
		return nil
	}

	// Step 2: if we're here, we need to create it

	// get original infrastructure object
	infra, err := external.Get(u.managementClusterClient, &ref, u.clusterNamespace)
	if err != nil {
		return err
	}

	// prep the replacement
	infra.SetResourceVersion("")
	infra.SetName(replacementKey.Name)

	ownerRefs := filterOutMachineOwners(infra.GetOwnerReferences())
	infra.SetOwnerReferences(ownerRefs)

	unstructured.RemoveNestedField(infra.UnstructuredContent(), "spec", "providerID")

	// Convert to a runtime.Object in case we need to patch, so we don't have to type assert after patching
	var toCreate runtime.Object = infra

	if u.infrastructurePatch != nil {
		toCreate, err = patchRuntimeObject(infra, u.infrastructurePatch)
		if err != nil {
			return errors.Wrap(err, "error patching infrastructure resource")
		}
	}

	// create the replacement infrastructure object
	err = u.managementClusterClient.Create(context.TODO(), toCreate)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (u *ControlPlaneUpgrader) listMachines() ([]*clusterv1.Machine, error) {
	labels := ctrlclient.MatchingLabels{
		clusterv1.MachineClusterLabelName:      u.clusterName,
		clusterv1.MachineControlPlaneLabelName: "true",
	}
	listOptions := []ctrlclient.ListOption{
		labels,
		ctrlclient.InNamespace(u.clusterNamespace),
	}
	machines := &clusterv1.MachineList{}

	u.log.Info("Listing machines", "labelSelector", labels)
	err := u.managementClusterClient.List(context.TODO(), machines, listOptions...)
	if err != nil {
		return nil, errors.Wrap(err, "error listing machines")
	}

	var ret []*clusterv1.Machine
	for i := range machines.Items {
		m := machines.Items[i]
		if m.DeletionTimestamp.IsZero() {
			ret = append(ret, &m)
		}
	}

	return ret, nil
}

type etcdMembersResponse struct {
	Members []etcdMember `json:"members"`
}

type etcdMember struct {
	ID         uint64   `json:"ID"`
	Name       string   `json:"name"`
	ClientURLs []string `json:"clientURLs"`
}

func (u *ControlPlaneUpgrader) listEtcdMembers(timeout time.Duration) ([]etcdMember, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	stdout, _, err := u.etcdctl(ctx, "member list -w json")
	if err != nil {
		return []etcdMember{}, err
	}

	var resp etcdMembersResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return []etcdMember{}, errors.Wrap(err, "unable to parse etcdctl member list json output")
	}

	return resp.Members, nil
}

func (u *ControlPlaneUpgrader) oldNodeToEtcdMemberId(timeout time.Duration) error {
	members, err := u.listEtcdMembers(timeout)
	if err != nil {
		return err
	}

	m := make(map[string]string)
	for _, member := range members {
		// etcd expects member IDs in hex, so convert to base 16
		id := strconv.FormatUint(member.ID, 16)
		m[member.Name] = id
	}

	u.oldNodeToEtcdMember = m

	return nil
}

// deleteEtcdMember deletes the old etcd member
func (u *ControlPlaneUpgrader) deleteEtcdMember(timeout time.Duration, etcdMemberId string) error {
	u.log.Info("Deleting etcd member", "id", etcdMemberId)
	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	_, _, err := u.etcdctl(ctx, "member", "remove", etcdMemberId)
	return err
}

func (u *ControlPlaneUpgrader) listEtcdPods() ([]v1.Pod, error) {
	// get pods in kube-system with label component=etcd
	list, err := u.targetKubernetesClient.CoreV1().Pods(metav1.NamespaceSystem).List(metav1.ListOptions{LabelSelector: "component=etcd"})
	if err != nil {
		return []v1.Pod{}, errors.Wrap(err, "error listing pods")
	}
	return list.Items, nil
}

func (u *ControlPlaneUpgrader) etcdctl(ctx context.Context, args ...string) (string, string, error) {
	pods, err := u.listEtcdPods()
	if err != nil {
		return "", "", err
	}
	if len(pods) == 0 {
		return "", "", errors.New("found 0 etcd pods")
	}

	var (
		stdout, stderr string
	)

	// Try all etcd pods. Return as soon as we get a successful result.
	for _, pod := range pods {
		stdout, stderr, err = u.etcdctlForPod(ctx, &pod, args...)
		if err == nil {
			return stdout, stderr, nil
		}
	}
	return stdout, stderr, err
}

func (u *ControlPlaneUpgrader) etcdctlForPod(ctx context.Context, pod *v1.Pod, args ...string) (string, string, error) {
	u.log.Info("Running etcdctl", "pod", pod.Name, "args", strings.Join(args, " "))

	ip := pod.Status.PodIP
	if ip == "" {
		ip = "127.0.0.1"
	}
	endpoint := fmt.Sprintf("https://%s:2379", ip)

	fullArgs := []string{
		"ETCDCTL_API=3",
		"etcdctl",
		"--cacert", etcdCACertFile,
		"--cert", etcdCertFile,
		"--key", etcdKeyFile,
		"--endpoints", endpoint,
	}

	fullArgs = append(fullArgs, args...)

	opts := kubernetes2.PodExecInput{
		RestConfig:       u.targetRestConfig,
		KubernetesClient: u.targetKubernetesClient,
		Namespace:        pod.Namespace,
		Name:             pod.Name,
		Command: []string{
			"sh",
			"-c",
			strings.Join(fullArgs, " "),
		},
	}

	opts.Command = append(opts.Command, args...)

	stdout, stderr, err := kubernetes2.PodExec(ctx, opts)

	// TODO figure out how we want logs to show up in this library
	u.log.Info(fmt.Sprintf("etcdctl stdout: %s", stdout))
	u.log.Info(fmt.Sprintf("etcdctl stderr: %s", stderr))

	return stdout, stderr, err
}

func (u *ControlPlaneUpgrader) updateKubeadmConfigMap(f func(in *v1.ConfigMap) (*v1.ConfigMap, error)) error {
	original, err := u.targetKubernetesClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(kubeadmConfigMapName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "error getting kubeadm configmap from target cluster")
	}

	updated, err := f(original)
	if err != nil {
		return err
	}

	if _, err = u.targetKubernetesClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Update(updated); err != nil {
		return errors.Wrap(err, "error updating kubeadm ConfigMap")
	}

	return nil
}

// updateKubeadmKubernetesVersion updates the Kubernetes version stored in the kubeadm configmap. This is
// required so that new Machines joining the cluster use the correct Kubernetes version as part of the upgrade.
func updateKubeadmKubernetesVersion(original *v1.ConfigMap, version string) (*v1.ConfigMap, error) {
	cm := original.DeepCopy()

	clusterConfig := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(cm.Data["ClusterConfiguration"]), &clusterConfig); err != nil {
		return nil, errors.Wrap(err, "error decoding kubeadm map ClusterConfiguration")
	}

	clusterConfig["kubernetesVersion"] = version

	updated, err := yaml.Marshal(clusterConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error encoding kubeadm configmap ClusterConfiguration")
	}

	cm.Data["ClusterConfiguration"] = string(updated)

	return cm, nil
}

func (u *ControlPlaneUpgrader) GetNodeFromProviderID(providerID string) *v1.Node {
	node, ok := u.providerIDsToNodes[providerID]
	if ok {
		return node
	}
	return nil
}

// UpdateProviderIDsToNodes retrieves a map that pairs a providerID to the node by listing all Nodes
// providerID : Node
func (u *ControlPlaneUpgrader) UpdateProviderIDsToNodes() error {
	u.log.Info("Updating provider IDs to nodes")
	nodes, err := u.targetKubernetesClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing nodes")
	}

	pairs := make(map[string]*v1.Node)
	for i := range nodes.Items {
		node := nodes.Items[i]
		id := ""
		providerID, err := noderefutil.NewProviderID(node.Spec.ProviderID)
		if err == nil {
			id = providerID.ID()
		} else {
			u.log.Error(err, "failed to parse provider id", "id", node.Spec.ProviderID, "node", node.Name)
			// unable to parse provider ID with whitelist of provider ID formats. Use original provider ID
			id = node.Spec.ProviderID
		}
		pairs[id] = &node
	}

	u.providerIDsToNodes = pairs

	return nil
}

func (u *ControlPlaneUpgrader) waitForMachineWithProviderID(ctx context.Context, ns, name string) (string, error) {
	log := u.log.WithValues("namespace", ns, "name", name)
	log.Info("Waiting for machine to have a provider id")
	var providerID string
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		machine := &clusterv1.Machine{}
		if err := u.managementClusterClient.Get(context.TODO(), ctrlclient.ObjectKey{Name: name, Namespace: ns}, machine); err != nil {
			log.Error(err, "Error getting machine, will try again")
			return false, nil
		}

		if machine.Spec.ProviderID == nil {
			return false, nil
		}

		providerID = *machine.Spec.ProviderID
		if providerID != "" {
			log.Info("Got provider id", "provider-id", providerID)
			return true, nil
		}
		return false, nil
	}, ctx.Done())

	if err != nil {
		return "", errors.Wrap(err, "timed out waiting for machine provider id")
	}

	return providerID, nil
}

func (u *ControlPlaneUpgrader) waitForNodeWithProviderID(ctx context.Context, rawProviderID string) (*v1.Node, error) {
	u.log.Info("Waiting for node", "provider-id", rawProviderID)
	var matchingNode v1.Node
	providerID, err := noderefutil.NewProviderID(rawProviderID)
	if err != nil {
		return nil, err
	}

	err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		nodes, err := u.targetKubernetesClient.CoreV1().Nodes().List(metav1.ListOptions{})
		if err != nil {
			u.log.Error(err, "Error listing nodes in target cluster, will try again")
			return false, nil
		}
		for _, node := range nodes.Items {
			nodeID, err := noderefutil.NewProviderID(node.Spec.ProviderID)
			if err != nil {
				u.log.Error(err, "unable to process node's provider ID", "node", node.Name, "provider-id", node.Spec.ProviderID)
				// Continue instead of returning so we can process all the nodes in the list
				continue
			}
			if providerID.Equals(nodeID) {
				u.log.Info("Found node", "name", node.Name, "provider-id", providerID.String())
				matchingNode = node
				return true, nil
			}
		}

		return false, nil
	}, ctx.Done())

	if err != nil {
		return nil, errors.Wrap(err, "timed out waiting for matching node")
	}

	return &matchingNode, nil
}

func (u *ControlPlaneUpgrader) waitForNodeReady(ctx context.Context, node *v1.Node) error {
	// wait for NodeReady
	err := wait.PollImmediateUntil(15*time.Second, func() (bool, error) {
		ready := u.isReady(node.Name)
		return ready, nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "components on node %s are not ready", node.Name)
	}
	return nil
}

func (u *ControlPlaneUpgrader) waitForNodeWithMasterLabel(ctx context.Context, node *v1.Node) error {
	err := wait.PollImmediateUntil(15*time.Second, func() (bool, error) {
		return u.hasMasterNodeLabel(node.Name), nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "node %s does not have master label %q", node.Name, LabelNodeRoleMaster)
	}
	return nil
}

func (u *ControlPlaneUpgrader) hasMasterNodeLabel(nodeName string) bool {
	log := u.log.WithValues("node", nodeName)
	log.Info("Verifying node has master label", "label", LabelNodeRoleMaster)

	node, err := u.targetKubernetesClient.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Info("Node not found yet")
		return false
	}
	if err != nil {
		log.Error(err, "error getting node")
		return false
	}

	_, ok := node.GetLabels()[LabelNodeRoleMaster]
	return ok
}

func (u *ControlPlaneUpgrader) isReady(nodeName string) bool {
	u.log.Info("Component health check for node", "name", nodeName)

	components := []string{"etcd", "kube-apiserver", "kube-scheduler", "kube-controller-manager"}
	requiredConditions := sets.NewString("PodScheduled", "Initialized", "Ready", "ContainersReady")

	for _, component := range components {
		foundConditions := sets.NewString()

		podName := fmt.Sprintf("%s-%v", component, nodeName)
		log := u.log.WithValues("pod", podName)

		log.Info("Getting pod")
		pod, err := u.targetKubernetesClient.CoreV1().Pods(metav1.NamespaceSystem).Get(podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			log.Info("Pod not found yet")
			return false
		}
		if err != nil {
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

// extractKubeadmConfigMapClusterStatus returns the ClusterStatus field from the kubeadm ConfigMap as an
// *unstructured.Unstructured.
func extractKubeadmConfigMapClusterStatus(in *v1.ConfigMap) (*unstructured.Unstructured, error) {
	clusterStatus := &unstructured.Unstructured{}

	rawClusterStatus, ok := in.Data["ClusterStatus"]
	if !ok {
		return nil, errors.New("ClusterStatus not found in kubeadm ConfigMap")
	}
	if err := yaml.Unmarshal([]byte(rawClusterStatus), &clusterStatus); err != nil {
		return nil, errors.Wrap(err, "error decoding kubeadm ClusterStatus object")
	}

	return clusterStatus, nil
}

// removeNodeFromKubeadmConfigMapClusterStatusAPIEndpoints removes an entry from ClusterStatus.APIEndpoints in the kubeadm
// ConfigMap.
func removeNodeFromKubeadmConfigMapClusterStatusAPIEndpoints(original *v1.ConfigMap, nodeName string) (*v1.ConfigMap, error) {
	cm := original.DeepCopy()

	clusterStatus, err := extractKubeadmConfigMapClusterStatus(original)
	if err != nil {
		return nil, err
	}

	endpoints, _, err := unstructured.NestedMap(clusterStatus.UnstructuredContent(), "apiEndpoints")
	if err != nil {
		return nil, err
	}

	// Remove node
	delete(endpoints, nodeName)

	err = unstructured.SetNestedMap(clusterStatus.UnstructuredContent(), endpoints, "apiEndpoints")
	if err != nil {
		return nil, err
	}

	updated, err := yaml.Marshal(clusterStatus)
	if err != nil {
		return nil, errors.Wrap(err, "error encoding kubeadm ClusterStatus object")
	}

	cm.Data["ClusterStatus"] = string(updated)

	return cm, nil
}

// reconcileKubeadmConfigMapClusterStatusAPIEndpoints reconciles ClusterStatus.APIEndpoints in the kubeadm ConfigMap by
// comparing the active Nodes with the entries in the APIEndpoints map. Any map entry that does not have a matching Node
// is removed.
func reconcileKubeadmConfigMapClusterStatusAPIEndpoints(in *v1.ConfigMap, nodeList *v1.NodeList) (*v1.ConfigMap, error) {
	cm := in.DeepCopy()

	clusterStatus, err := extractKubeadmConfigMapClusterStatus(in)
	if err != nil {
		return nil, err
	}

	endpoints, _, err := unstructured.NestedMap(clusterStatus.UnstructuredContent(), "apiEndpoints")
	if err != nil {
		return nil, err
	}

	nodeNames := sets.NewString()
	for _, node := range nodeList.Items {
		nodeNames.Insert(node.Name)
	}

	for nodeName := range endpoints {
		if !nodeNames.Has(nodeName) {
			delete(endpoints, nodeName)
		}
	}

	err = unstructured.SetNestedMap(clusterStatus.UnstructuredContent(), endpoints, "apiEndpoints")
	if err != nil {
		return nil, err
	}

	updated, err := yaml.Marshal(clusterStatus)
	if err != nil {
		return nil, errors.Wrap(err, "error encoding kubeadm ClusterStatus object")
	}

	cm.Data["ClusterStatus"] = string(updated)

	return cm, nil

}
