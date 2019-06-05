// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
)

func main() {
	upgradeConfig := upgrade.Config{}

	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Upgrades Kubernetes clusters created by Cluster API.",
		RunE: func(_ *cobra.Command, _ []string) error {
			err := upgrade.ValidateArgs(upgradeConfig)
			if err != nil {
				logrus.Errorf("Failed to validate the arguments: %v", err)
				return err
			}

			// TODO: print the stack trace or handle the error.
			err = upgradeCluster(upgradeConfig)
			if err != nil {
				logrus.Errorf("Upgrade failed: %s", err.Error())
			} else {
				logrus.Infof("Upgrade completed successfully on cluster %s", upgradeConfig.TargetCluster.Name)
			}
			return err
		},
		SilenceUsage: true,
	}

	root.Flags().StringVar(&upgradeConfig.ManagementCluster.Kubeconfig, "kubeconfig",
		upgradeConfig.ManagementCluster.Kubeconfig, "The kubeconfig path for the management cluster(Required)")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.Namespace,
		"cluster-namespace", upgradeConfig.TargetCluster.Name, "The namespace of target cluster(Required)")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.Name, "cluster-name", upgradeConfig.TargetCluster.Name,
		"The name of target cluster(Required)")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.SecretRef, "ca-secret",
		upgradeConfig.TargetCluster.CAKeyPair.SecretRef, "TODO")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.ClusterField, "ca-field",
		"spec.providerSpec.value.caKeyPair", "The CA field in provider manifests(Optional)")
	root.Flags().StringVar(&upgradeConfig.KubernetesVersion, "kubernetes-version", upgradeConfig.KubernetesVersion,
		"Desired kubernetes version to upgrade to(Required)")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.UpgradeScope, "scope", "all",
		"Scope of upgrade - [controlplane/node/all](Optional, defaults to all)")
	root.Flags().StringVar(&upgradeConfig.TargetCluster.TargetApiEndpoint, "api-endpoint",
		upgradeConfig.TargetCluster.TargetApiEndpoint, "Target cluster's API endpoint(Optional)")

	if err := root.Execute(); err != nil {
		root.Usage()
		os.Exit(1)
	}
}

func upgradeCluster(config upgrade.Config) error {
	// @TODO Add Logging here
	// @TODO add Retry logic here
	// @TODO add state machine here

	//TODO convert scope to constant
	scope := config.TargetCluster.UpgradeScope
	if scope == "controlplane" || scope == "all" {
		controlPlaneUpgrader, err := upgrade.NewControlPlaneUpgrader(config)
		if err != nil {
			return errors.Wrap(err, "Failed to create the upgrader for control plane")
		}

		err = controlPlaneUpgrader.Upgrade()
		// TODO try to recover from Failure
		if err != nil {
			return errors.Wrap(err, "Failed to upgrade the control plane")
		}
	}

	if scope == "node" || scope == "all" {
		workerUpgrader, err := upgrade.NewMachineDeploymentUpgrader(config)
		if err != nil {
			return errors.Wrap(err, "Failed to create the upgrader for worker")
		}

		err = workerUpgrader.Upgrade()
		//TODO try to recover from failure
		if err != nil {
			return errors.Wrap(err, "Failed to upgrade the worker nodes")
		}
	}

	return nil
}
