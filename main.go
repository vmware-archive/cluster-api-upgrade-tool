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
		"", "The kubeconfig path for the management cluster (required)")
	root.MarkFlagRequired("kubeconfig")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.Namespace,
		"cluster-namespace", "", "The namespace of target cluster (required)")
	root.MarkFlagRequired("cluster-namespace")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.Name, "cluster-name", "",
		"The name of target cluster (required)")
	root.MarkFlagRequired("cluster-name")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.SecretRef, "ca-secret", "", "TODO")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.ClusterField, "ca-field",
		"spec.providerSpec.value.caKeyPair", "The CA field in provider manifests (optional)")

	root.Flags().StringVar(&upgradeConfig.KubernetesVersion, "kubernetes-version", "",
		"Desired kubernetes version to upgrade to (required)")
	root.MarkFlagRequired("kubernetes-version")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.UpgradeScope, "scope", "",
		"Scope of upgrade - [control-plane | machine-deployment] (required)")
	root.MarkFlagRequired("scope")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.TargetApiEndpoint, "api-endpoint",
		"", "Target cluster's API endpoint (optional)")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}


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
	case upgrade.ControlPlaneScope:
		upgrader, err = upgrade.NewControlPlaneUpgrader(log, config)
	case upgrade.MachineDeploymentScope:
		upgrader, err = upgrade.NewMachineDeploymentUpgrader(log, config)
	default:
		return errors.Errorf("invalid scope %q", config.TargetCluster.UpgradeScope)
	}

	if err != nil {
		return err
	}

	return upgrader.Upgrade()
}
