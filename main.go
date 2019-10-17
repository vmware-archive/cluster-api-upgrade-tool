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
	"k8s.io/apimachinery/pkg/util/yaml"
)

func newLogger() logr.Logger {
	log := logrus.New()
	log.Out = os.Stdout

	return logging.NewLogrusLoggerAdapter(log)
}

func main() {
	configFile := ""
	upgradeConfig := upgrade.Config{}

	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Upgrades Kubernetes clusters created by Cluster API.",
		RunE: func(_ *cobra.Command, _ []string) error {
			if configFile != "" {
				reader, err := os.Open(configFile)
				if err != nil {
					return errors.Wrap(err, "error opening config file")
				}
				defer reader.Close()

				const bufferSize = 512
				decoder := yaml.NewYAMLOrJSONDecoder(reader, bufferSize)
				if err := decoder.Decode(&upgradeConfig); err != nil {
					return errors.Wrap(err, "error parsing config file")
				}

				// TODO either merge in other flag values after decoding, or raise an error if the user specifies
				// --config with any other flags
			}

			upgrader, err := upgrade.NewControlPlaneUpgrader(newLogger(), upgradeConfig)
			if err != nil {
				return err
			}

			return upgrader.Upgrade()

		},
		SilenceUsage: true,
	}

	root.Flags().StringVar(
		&configFile,
		"config",
		"",
		"Path to a config file in yaml or json format",
	)

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

	root.Flags().StringVar(
		&upgradeConfig.TargetCluster.Name,
		"cluster-name",
		"",
		"The name of target cluster (required)",
	)

	root.Flags().StringVar(
		&upgradeConfig.KubernetesVersion,
		"kubernetes-version",
		"",
		"Desired kubernetes version to upgrade to (required)",
	)

	root.Flags().StringVar(
		&upgradeConfig.UpgradeID,
		"upgrade-id",
		"",
		"Unique identifier used to resume a partial upgrade (optional)",
	)

	if err := root.Execute(); err != nil {
		// Print a stack trace, if possible. We may end up with the error message printed twice,
		// but the stack trace can be invaluable, so we'll accept this for the time being.
		fmt.Printf("%+v\n", err)
		os.Exit(1)
	}
}
