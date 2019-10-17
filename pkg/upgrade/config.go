// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import "regexp"

var upgradeIDNameSuffixRegex = regexp.MustCompile(`upgrade\.[0-9]+$`)
var upgradeIDInputRegex = regexp.MustCompile("^[0-9]+$")

// Config contains all the configurations necessary to upgrade a Kubernetes cluster.
type Config struct {
	ManagementCluster ManagementClusterConfig       `json:"managementCluster"`
	TargetCluster     TargetClusterConfig           `json:"targetCluster"`
	MachineUpdates    MachineUpdateConfig           `json:"machineUpdates"`
	KubernetesVersion string                        `json:"kubernetesVersion"`
	UpgradeID         string                        `json:"upgradeID"`
	MachineDeployment MachineDeploymentUpdateConfig `json:"machineDeployment"`
}

// ManagementClusterConfig is the Kubeconfig and relevant information to connect to the management cluster of the worker cluster being upgraded.
type ManagementClusterConfig struct {
	// Kubeconfig is a path to a kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Context    string `json:"context"`
}

// TargetClusterConfig are all the necessary configs of the Kubernetes cluster being upgraded.
type TargetClusterConfig struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// MachineUpdateConfig contains the configuration of the machine desired.
type MachineUpdateConfig struct {
	Image ImageUpdateConfig `json:"image,omitempty"`
}

// ImageUpdateConfig is something
type ImageUpdateConfig struct {
	ID    string `json:"id"`
	Field string `json:"field"`
}

// MachineDeploymentUpdateConfig contains details for specifying which machine deployment(s) to upgrade.
type MachineDeploymentUpdateConfig struct {
	Name          string `json:"name"`
	LabelSelector string `json:"labelSelector"`
}
