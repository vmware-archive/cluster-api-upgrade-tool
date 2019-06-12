// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/logging"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
)

func newLogger() logr.Logger {
	log := logrus.New()
	log.Out = os.Stdout

	return logging.NewLogrusLoggerAdapter(log)
}

func main() {
	upgradeConfig := upgrade.Config{}

	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Upgrades Kubernetes clusters created by Cluster API.",
		RunE: func(_ *cobra.Command, _ []string) error {
			err := upgrade.ValidateArgs(upgradeConfig)
			if err != nil {
				return err
			}

			return upgradeCluster(upgradeConfig)
		},
		SilenceUsage: true,
	}

	root.Flags().StringVar(&upgradeConfig.ManagementCluster.Kubeconfig, "kubeconfig",
		upgradeConfig.ManagementCluster.Kubeconfig, "The kubeconfig path for the management cluster (required)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.Namespace,
		"cluster-namespace", upgradeConfig.TargetCluster.Name, "The namespace of target cluster (required)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.Name, "cluster-name", upgradeConfig.TargetCluster.Name,
		"The name of target cluster (required)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.SecretRef, "ca-secret",
		upgradeConfig.TargetCluster.CAKeyPair.SecretRef, "TODO")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.ClusterField, "ca-field",
		"spec.providerSpec.value.caKeyPair", "The CA field in provider manifests (optional)")

	root.Flags().StringVar(&upgradeConfig.KubernetesVersion, "kubernetes-version", upgradeConfig.KubernetesVersion,
		"Desired kubernetes version to upgrade to (required)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.UpgradeScope, "scope", upgradeConfig.TargetCluster.UpgradeScope,
		"Scope of upgrade - [control-plane | machine-deployment] (required)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.TargetApiEndpoint, "api-endpoint",
		upgradeConfig.TargetCluster.TargetApiEndpoint, "Target cluster's API endpoint (optional)")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

const (
	upgradeScopeControlPlane      = "control-plane"
	upgradeScopeMachineDeployment = "machine-deployment"
)

type upgrader interface {
	Upgrade() error
}

func upgradeCluster(config upgrade.Config) error {
	var (
		log      = newLogger()
		upgrader upgrader
		err      error
	)

	switch config.TargetCluster.UpgradeScope {
	case upgradeScopeControlPlane:
		upgrader, err = upgrade.NewControlPlaneUpgrader(log, config)
	case upgradeScopeMachineDeployment:
		upgrader, err = upgrade.NewMachineDeploymentUpgrader(log, config)
	default:
		return errors.Errorf("invalid scope %q", config.TargetCluster.UpgradeScope)
	}

	if err != nil {
		return err
	}

	return upgrader.Upgrade()
}
