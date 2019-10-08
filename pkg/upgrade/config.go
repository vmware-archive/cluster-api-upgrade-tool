// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"github.com/blang/semver"
	"github.com/pkg/errors"
)

const (
	ControlPlaneScope      = "control-plane"
	MachineDeploymentScope = "machine-deployment"
)

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
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	UpgradeScope string `json:"scope"`
}

func (t *TargetClusterConfig) UpgradeScopes() []string {
	return []string{ControlPlaneScope, MachineDeploymentScope}
}

// MachineUpdateConfig contains the configuration of the machine desired.
type MachineUpdateConfig struct {
	Image        ImageUpdateConfig `json:"image,omitempty"`
	MachineClass string            `json:"machineClass,omitempty"`
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

// ValidateArgs validates the configuration passed in and returns the first validation error encountered.
func ValidateArgs(config Config) error {
	validUpgradeScope := false
	for _, scope := range config.TargetCluster.UpgradeScopes() {
		if config.TargetCluster.UpgradeScope == scope {
			validUpgradeScope = true
			break
		}
	}
	if !validUpgradeScope {
		return errors.Errorf("invalid upgrade scope, must be one of %v", config.TargetCluster.UpgradeScopes())
	}

	if _, err := semver.ParseTolerant(config.KubernetesVersion); err != nil {
		return errors.Errorf("Invalid Kubernetes version: %q", config.KubernetesVersion)
	}

	if (config.MachineUpdates.Image.ID == "" && config.MachineUpdates.Image.Field != "") ||
		(config.MachineUpdates.Image.ID != "" && config.MachineUpdates.Image.Field == "") {
		return errors.New("when specifying image id, image field is required (and vice versa)")
	}

	if config.MachineDeployment.Name != "" && config.MachineDeployment.LabelSelector != "" {
		return errors.New("you may only set one of machine deployment name and label selector, but not both")
	}

	return nil
}
