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
		"", "The CA field in provider manifests, 'spec.providerSpec.value.caKeyPair' for the AWS provider (optional)")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.KubeconfigSecretRef, "kubeconfig-secret", "",
		"The name of the secret the kubeconfig is stored in. Assumed to be in the same namespace as the cluster object.")

	root.Flags().StringVar(&upgradeConfig.KubernetesVersion, "kubernetes-version", "",
		"Desired kubernetes version to upgrade to (required)")
	root.MarkFlagRequired("kubernetes-version")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.UpgradeScope, "scope", "",
		"Scope of upgrade - [control-plane | machine-deployment] (required)")
	root.MarkFlagRequired("scope")

	root.Flags().StringVar(&upgradeConfig.TargetCluster.CAKeyPair.APIEndpoint, "api-endpoint",
		"", "Target cluster's API endpoint and port. For example: https://example.com:6443. Required with --ca-secret OR --ca-field. Ignored with --kubeconfig-secret-ref.")

	root.Flags().StringVar(&upgradeConfig.MachineUpdates.Image.ID, "image-id",
		"", "The provider-specific image identifier to use when booting a machine (optional)")

	root.Flags().StringVar(&upgradeConfig.MachineUpdates.Image.Field, "image-field",
		"", "The image identifier field in provider manifests (optional)")

	root.Flags().StringVar(&upgradeConfig.UpgradeID, "upgrade-id", "",
		"Unique identifier used to resume a partial upgrade (optional)")

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

	infoMessage := fmt.Sprintf("Rerun with `--upgrade-id=%s` if this upgrade fails midway and you want to retry", config.UpgradeID)
	log.Info(infoMessage)
	return upgrader.Upgrade()
}
