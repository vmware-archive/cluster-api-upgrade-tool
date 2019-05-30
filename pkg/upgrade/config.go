// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"github.com/pkg/errors"
)

type Config struct {
	ManagementCluster ManagementClusterConfig `json:"managementCluster"`
	TargetCluster     TargetClusterConfig     `json:"targetCluster"`
	MachineUpdates    MachineUpdateConfig     `json:"machineUpdates"`
	KubernetesVersion string                  `json:"kubernetesVersion"`
}

type ManagementClusterConfig struct {
	Kubeconfig string `json:"kubeconfig"`
	Context    string `json:"context"`
}

type TargetClusterConfig struct {
	Namespace         string        `json:"namespace"`
	Name              string        `json:"name"`
	CAKeyPair         KeyPairConfig `json:"caKeyPair"`
	TargetApiEndpoint string        `json:"api-endpoint"`
	UpgradeScope      string        `json:"scope"`
}

type KeyPairConfig struct {
	SecretRef    string `json:"secretRef,omitempty"`
	ClusterField string `json:"clusterField,omitempty"`
}

type MachineUpdateConfig struct {
	Image        *ImageUpdateConfig `json:"image,omitempty"`
	MachineClass string             `json:"machineClass,omitempty"`
}

type ImageUpdateConfig struct {
	ID    string `json:"id"`
	Field string `json:"field"`
}

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
	//TODO - validate the that kubernetes version follows the correct format. vx.xx.xx
	if config.KubernetesVersion == "" {
		return errors.New("kubernetes version is required")
	}
	return nil
}
