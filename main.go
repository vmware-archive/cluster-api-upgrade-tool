// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
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
	var scope string
	upgradeConfig := upgrade.Config{}

	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Upgrades Kubernetes clusters created by Cluster API.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return upgradeCluster(scope, upgradeConfig)
		},
		SilenceUsage: true,
	}

	root.Flags().StringVar(
		&upgradeConfig.ManagementCluster.Kubeconfig,
		"kubeconfig",
		"",
		"The kubeconfig path for the management cluster",
	)

	root.Flags().StringVar(
		&upgradeConfig.TargetCluster.Namespace,
		"cluster-namespace",
		"",
		"The namespace of target cluster (required)",
	)
	if err := root.MarkFlagRequired("cluster-namespace"); err != nil {
		fmt.Printf("Unable to mark cluster-namespace as a required flag: %v\n", err)
		os.Exit(1)
	}

	root.Flags().StringVar(
		&upgradeConfig.TargetCluster.Name,
		"cluster-name",
		"",
		"The name of target cluster (required)",
	)
	if err := root.MarkFlagRequired("cluster-name"); err != nil {
		fmt.Printf("Unable to mark cluster-name as a required flag: %v\n", err)
		os.Exit(1)
	}

	root.Flags().StringVar(
		&upgradeConfig.KubernetesVersion,
		"kubernetes-version",
		"",
		"Desired kubernetes version to upgrade to (required)",
	)
	if err := root.MarkFlagRequired("kubernetes-version"); err != nil {
		fmt.Printf("Unable to mark kubernetes-version as a required flag: %v\n", err)
		os.Exit(1)
	}

	root.Flags().StringVar(
		&scope,
		"scope",
		"",
		"Scope of upgrade - [control-plane | machine-deployment] (required)",
	)
	if err := root.MarkFlagRequired("scope"); err != nil {
		fmt.Printf("Unable to mark scope as a required flag: %v\n", err)
		os.Exit(1)
	}

	root.Flags().StringVar(
		&upgradeConfig.MachineUpdates.Image.ID,
		"image-id",
		"",
		"The provider-specific image identifier to use when booting a machine (optional)",
	)

	root.Flags().StringVar(
		&upgradeConfig.MachineUpdates.Image.Field,
		"image-field",
		"",
		"The image identifier field in provider manifests (optional)",
	)

	root.Flags().StringVar(
		&upgradeConfig.UpgradeID,
		"upgrade-id",
		"",
		"Unique identifier used to resume a partial upgrade (optional)",
	)

	root.Flags().StringVar(
		&upgradeConfig.MachineDeployment.Name,
		"machine-deployment-name",
		"",
		"Name of a single machine deployment to upgrade",
	)

	root.Flags().StringVar(
		&upgradeConfig.MachineDeployment.LabelSelector,
		"machine-deployment-selector",
		"",
		"Label selector used to find machine deployments to upgrade",
	)

	if err := root.Execute(); err != nil {
		// Print a stack trace, if possible. We may end up with the error message printed twice,
		// but the stack trace can be invaluable, so we'll accept this for the time being.
		fmt.Printf("%+v\n", err)
		os.Exit(1)
	}
}

type upgrader interface {
	Upgrade() error
}

const (
	controlPlaneScope      = "control-plane"
	machineDeploymentScope = "machine-deployment"
)

func upgradeCluster(scope string, config upgrade.Config) error {
	var (
		log      = newLogger()
		upgrader upgrader
		err      error
	)

	validScopes := []string{controlPlaneScope, machineDeploymentScope}

	switch scope {
	case controlPlaneScope:
		upgrader, err = upgrade.NewControlPlaneUpgrader(log, config)
	case machineDeploymentScope:
		upgrader, err = upgrade.NewMachineDeploymentUpgrader(log, config)
	default:
		return errors.Errorf("invalid upgrade scope, must be one of %v", validScopes)
	}

	if err != nil {
		return err
	}

	return upgrader.Upgrade()
}
