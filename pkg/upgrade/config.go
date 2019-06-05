// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"fmt"

	"github.com/blang/semver"
	"github.com/pkg/errors"
)

// Config contains all the configurations necessary to upgrade a Kubernetes cluster.
type Config struct {
	ManagementCluster ManagementClusterConfig `json:"managementCluster"`
	TargetCluster     TargetClusterConfig     `json:"targetCluster"`
	MachineUpdates    MachineUpdateConfig     `json:"machineUpdates"`
	KubernetesVersion string                  `json:"kubernetesVersion"`
}

// ManagementClusterConfig is the Kubeconfig and relevant information to connect to the management cluster of the worker cluster being upgraded.
type ManagementClusterConfig struct {
	Kubeconfig string `json:"kubeconfig"`
	Context    string `json:"context"`
}

// TargetClusterConfig are all the necessary configs of the Kubernetes cluster being upgraded.
type TargetClusterConfig struct {
	Namespace         string        `json:"namespace"`
	Name              string        `json:"name"`
	CAKeyPair         KeyPairConfig `json:"caKeyPair"`
	TargetApiEndpoint string        `json:"api-endpoint"`
	UpgradeScope      string        `json:"scope"`
}

// KeyPairConfig is something
type KeyPairConfig struct {
	SecretRef    string `json:"secretRef,omitempty"`
	ClusterField string `json:"clusterField,omitempty"`
}

// MachineUpdateConfig contains the configuration of the machine desired.
type MachineUpdateConfig struct {
	Image        *ImageUpdateConfig `json:"image,omitempty"`
	MachineClass string             `json:"machineClass,omitempty"`
}

// ImageUpdateConfig is something
type ImageUpdateConfig struct {
	ID    string `json:"id"`
	Field string `json:"field"`
}

// ValidateArgs validates the configuration passed in and returns the first validation error encountered.
func ValidateArgs(config Config) error {
	if config.TargetCluster.Namespace == "" {
		return errors.New("target cluster namespace is required")
	}
	if config.TargetCluster.Name == "" {
		return errors.New("target cluster name is required")
	}
	if config.TargetCluster.CAKeyPair.ClusterField != "" && config.TargetCluster.CAKeyPair.SecretRef != "" {
		return errors.New("only one of key pair cluster field and secret ref may be set")
	}
	if config.TargetCluster.CAKeyPair.ClusterField == "" && config.TargetCluster.CAKeyPair.SecretRef == "" {
		return errors.New("one of key pair cluster field or secret ref must be set")
	}
	if config.KubernetesVersion == "" {
		return errors.New("kubernetes version is required")
	}
	if _, err := semver.ParseTolerant(config.KubernetesVersion); err != nil {
		return fmt.Errorf("Invalid Kubernetes version: %v", config.KubernetesVersion)
	}
	return nil
}
