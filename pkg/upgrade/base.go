// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"fmt"
	"io/ioutil"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	kubernetes2 "github.com/vmware/cluster-api-upgrade-tool/pkg/internal/kubernetes"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clusterapiv1alpha2client "sigs.k8s.io/cluster-api/cmd/clusterctl/clusterdeployer/clusterclient"
	"sigs.k8s.io/cluster-api/controllers/noderefutil"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type base struct {
	log                        logr.Logger
	userVersion                semver.Version
	desiredVersion             semver.Version
	clusterNamespace           string
	clusterName                string
	managementClusterAPIClient clusterapiv1alpha2client.Client
	ctrlClient                 ctrlclient.Client
	targetRestConfig           *rest.Config
	targetKubernetesClient     kubernetes.Interface
	providerIDsToNodes         map[string]*v1.Node
	imageField, imageID        string
	upgradeID                  string
	machineGetter              machineGetter
}

func newBase(log logr.Logger, config Config) (*base, error) {
	var userVersion, desiredVersion semver.Version

	if config.KubernetesVersion != "" {
		v, err := semver.ParseTolerant(config.KubernetesVersion)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing kubernetes version %q", config.KubernetesVersion)
		}
		userVersion = v
		desiredVersion = v
	}

	log.Info("Creating management rest config")
	managementRestConfig, err := kubernetes2.NewRestConfig(config.ManagementCluster.Kubeconfig, config.ManagementCluster.Context)
	if err != nil {
		return nil, err
	}

	log.Info("Creating controller runtime client")
	ctrlRuntimeClient, err := ctrlclient.New(managementRestConfig, ctrlclient.Options{}) // @TODO use NewCached() from cluster-api?
	if err != nil {
		return nil, errors.Wrap(err, "error creating controller runtime client")
	}

	log.Info("Creating management cluster api client")
	kubeconfig, err := ioutil.ReadFile(config.ManagementCluster.Kubeconfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating management cluster api client")
	}
	managementClusterAPIClient, err := clusterapiv1alpha2client.New(string(kubeconfig))
	if err != nil {
		log.Info("HIT HIT HIT", "KUBECONFIG", config.ManagementCluster.Kubeconfig)
		return nil, errors.Wrap(err, "error creating management cluster api client")
	}

	log.Info("Retrieving cluster from management cluster", "cluster-namespace", config.TargetCluster.Namespace, "cluster-name", config.TargetCluster.Name)
	cluster, err := managementClusterAPIClient.GetCluster(config.TargetCluster.Name, config.TargetCluster.Namespace)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	log.Info("Creating management kubernetes client")
	managementKubernetesClient, err := kubernetes.NewForConfig(managementRestConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating management kubernetes client")
	}

	var targetRestConfig *rest.Config
	secretClient := managementKubernetesClient.CoreV1().Secrets(cluster.GetNamespace())
	// CAKeyPair should have been validated, but if not this is defining an order for us
	if config.TargetCluster.CAKeyPair.KubeconfigSecretRef != "" {
		targetRestConfig, err = NewRestConfigFromKubeconfigSecretRef(secretClient, config.TargetCluster.CAKeyPair.KubeconfigSecretRef)
	} else if config.TargetCluster.CAKeyPair.SecretRef != "" {
		targetRestConfig, err = NewRestConfigFromCASecretRef(secretClient, config.TargetCluster.CAKeyPair.SecretRef, cluster.GetName(), config.TargetCluster.CAKeyPair.APIEndpoint)
	} else if config.TargetCluster.CAKeyPair.ClusterField != "" {
		targetRestConfig, err = NewRestConfigFromCAClusterField(cluster, config.TargetCluster.CAKeyPair.ClusterField, config.TargetCluster.CAKeyPair.APIEndpoint)
	}

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

	if config.UpgradeID == "" {
		config.UpgradeID = fmt.Sprintf("%d", time.Now().Unix())
	}

	infoMessage := fmt.Sprintf("Rerun with `--upgrade-id=%s` if this upgrade fails midway and you want to retry", config.UpgradeID)
	log.Info(infoMessage)

	return &base{
		log:                        log,
		userVersion:                userVersion,
		desiredVersion:             desiredVersion,
		clusterNamespace:           config.TargetCluster.Namespace,
		clusterName:                config.TargetCluster.Name,
		managementClusterAPIClient: managementClusterAPIClient,
		ctrlClient:                 ctrlRuntimeClient,
		targetRestConfig:           targetRestConfig,
		targetKubernetesClient:     targetKubernetesClient,
		imageField:                 config.MachineUpdates.Image.Field,
		imageID:                    config.MachineUpdates.Image.ID,
		upgradeID:                  config.UpgradeID,
		machineGetter:              &GetMachine{ctrlRuntimeClient},
	}, nil
}

func (u *base) GetNodeFromProviderID(providerID string) *v1.Node {
	node, ok := u.providerIDsToNodes[providerID]
	if ok {
		return node
	}
	return nil
}

// UpdateProviderIDsToNodes retrieves a map that pairs a providerID to the node by listing all Nodes
// providerID : Node
func (u *base) UpdateProviderIDsToNodes() error {
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
			u.log.Error(err, "failed to parse provider id", "id", node.Spec.ProviderID)
			// unable to parse provider ID with whitelist of provider ID formats. Use original provider ID
			id = node.Spec.ProviderID
		}
		pairs[id] = &node
	}

	u.providerIDsToNodes = pairs

	return nil
}

var unsetVersion semver.Version
