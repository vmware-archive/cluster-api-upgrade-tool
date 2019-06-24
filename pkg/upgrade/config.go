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
	ManagementCluster ManagementClusterConfig `json:"managementCluster"`
	TargetCluster     TargetClusterConfig     `json:"targetCluster"`
	MachineUpdates    MachineUpdateConfig     `json:"machineUpdates"`
	KubernetesVersion string                  `json:"kubernetesVersion"`
	UpgradeID         string                  `json:"upgradeID"`
}

// ManagementClusterConfig is the Kubeconfig and relevant information to connect to the management cluster of the worker cluster being upgraded.
type ManagementClusterConfig struct {
	// Kubeconfig is a path to a kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Context    string `json:"context"`
}

// TargetClusterConfig are all the necessary configs of the Kubernetes cluster being upgraded.
type TargetClusterConfig struct {
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"`
	CAKeyPair    KeyPairConfig `json:"caKeyPair"`
	UpgradeScope string        `json:"scope"`
}

func (t *TargetClusterConfig) UpgradeScopes() []string {
	return []string{ControlPlaneScope, MachineDeploymentScope}
}

// KeyPairConfig is something
type KeyPairConfig struct {
	SecretRef           string `json:"secretRef,omitempty"`
	ClusterField        string `json:"clusterField,omitempty"`
	KubeconfigSecretRef string `json:"kubeconfigSecretRef,omitempty"`

	// APIEndpoint is a URL to a kube-apiserver.
	// This entry is used only if SecretRef or ClusterField is set.
	// APIEndpoint is ignored if KubeconfigSecretRef is set.
	APIEndpoint string `json:"apiEndpoint"`
}

func (k KeyPairConfig) validate() error {
	if k.SecretRef != "" && k.ClusterField != "" {
		return errors.New("cannot set both --ca-secret and --ca-field")
	}
	if k.SecretRef != "" && k.KubeconfigSecretRef != "" {
		return errors.New("cannot set both --ca-secret and --kubeconfig-secret-ref")
	}
	if k.ClusterField != "" && k.KubeconfigSecretRef != "" {
		return errors.New("cannot set both --ca-field and --kubeconfig-secret-ref")
	}
	if k.SecretRef == "" && k.ClusterField == "" && k.KubeconfigSecretRef == "" {
		return errors.New("must set one of [--ca-secret, --ca-field, or --kubeconfig-secret-ref]")
	}
	if k.SecretRef != "" && k.APIEndpoint == "" {
		return errors.New("must set --api-endpoint with --ca-secret")
	}
	if k.ClusterField != "" && k.APIEndpoint == "" {
		return errors.New("must set --api-endpoint with --ca-field")
	}
	return nil
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

// ValidateArgs validates the configuration passed in and returns the first validation error encountered.
func ValidateArgs(config Config) error {
	if err := config.TargetCluster.CAKeyPair.validate(); err != nil {
		return err
	}

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

	return nil
}
