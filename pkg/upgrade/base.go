// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"fmt"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	kubernetes2 "github.com/vmware/cluster-api-upgrade-tool/pkg/internal/kubernetes"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clusterapiv1alpha1client "sigs.k8s.io/cluster-api/pkg/client/clientset_generated/clientset/typed/cluster/v1alpha1"
)

type base struct {
	userVersion                semver.Version
	desiredVersion             semver.Version
	clusterNamespace           string
	clusterName                string
	managementClusterAPIClient clusterapiv1alpha1client.ClusterV1alpha1Interface
	targetRestConfig           *rest.Config
	targetKubernetesClient     kubernetes.Interface
	providerIDsToNodes         map[string]*v1.Node
}

func newBase(config Config) (*base, error) {
	var userVersion, desiredVersion semver.Version

	if config.KubernetesVersion != "" {
		v, err := semver.ParseTolerant(config.KubernetesVersion)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing kubernetes version %q", config.KubernetesVersion)
		}
		userVersion = v
		desiredVersion = v
	}

	logrus.Info("Creating management rest config")
	managementRestConfig, err := kubernetes2.NewRestConfig(config.ManagementCluster.Kubeconfig, config.ManagementCluster.Context)
	if err != nil {
		return nil, err
	}

	logrus.Info("Creating management cluster api client")
	managementClusterAPIClient, err := clusterapiv1alpha1client.NewForConfig(managementRestConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating management cluster api client")
	}

	logrus.Infof("Retrieving cluster %s/%s from management cluster", config.TargetCluster.Namespace, config.TargetCluster.Name)
	cluster, err := managementClusterAPIClient.Clusters(config.TargetCluster.Namespace).Get(config.TargetCluster.Name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	clusterEndPoint := config.TargetCluster.TargetApiEndpoint
	if len(cluster.Status.APIEndpoints) > 0 {
		clusterEndPoint = cluster.Status.APIEndpoints[0].Host
	}

	if clusterEndPoint == "" {
		return nil, errors.New("cluster has no api endpoints and its also not provided as an input")
	}

	// TODO .Port is 443, but for CAPA, the ELB is on 6443
	targetClusterURL := fmt.Sprintf("https://%s:%d", clusterEndPoint, 6443)
	logrus.Infof("Target cluster URL: %s", targetClusterURL)

	logrus.Infof("Creating management kubernetes client")
	managementKubernetesClient, err := kubernetes.NewForConfig(managementRestConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating management kubernetes client")
	}

	logrus.Info("Getting key pair")
	keyPair, err := newKeyPairGetter(managementKubernetesClient.CoreV1()).getKeyPair(cluster, config.TargetCluster.CAKeyPair)
	if err != nil {
		return nil, err
	}

	logrus.Info("Generating target rest config from key pair")
	targetRestConfig, err := restConfigFromKeyPair(cluster.GetName(), targetClusterURL, keyPair)
	if err != nil {
		return nil, err
	}

	logrus.Info("Creating target kubernetes client")
	targetKubernetesClient, err := kubernetes.NewForConfig(targetRestConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating target cluster client")
	}

	return &base{
		userVersion:                userVersion,
		desiredVersion:             desiredVersion,
		clusterNamespace:           config.TargetCluster.Namespace,
		clusterName:                config.TargetCluster.Name,
		managementClusterAPIClient: managementClusterAPIClient,
		targetRestConfig:           targetRestConfig,
		targetKubernetesClient:     targetKubernetesClient,
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
	logrus.Info("Updating provider IDs to nodes")
	nodes, err := u.targetKubernetesClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing nodes")
	}

	pairs := make(map[string]*v1.Node)
	for i := range nodes.Items {
		node := nodes.Items[i]
		providerID, err := kubernetes2.ParseProviderID(node.Spec.ProviderID)
		if err != nil {
			// unable to parse provider ID with whitelist of provider ID formats. Use original provider ID
			providerID = node.Spec.ProviderID
		}
		pairs[providerID] = &node
	}

	u.providerIDsToNodes = pairs

	return nil
}

var unsetVersion semver.Version
