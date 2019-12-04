// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/imdario/mergo"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/logging"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func newLogger() logr.Logger {
	log := logrus.New()
	log.Out = os.Stdout

	return logging.NewLogrusLoggerAdapter(log)
}

func main() {
	configFile := ""
	configFromFlags := upgrade.Config{
		MachineTimeout: metav1.Duration{Duration: 15 * time.Minute},
	}

	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Upgrades Kubernetes clusters created by Cluster API.",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Default the final config to coming from flags
			finalConfig := configFromFlags

			// Process the config file, if applicable
			if configFile != "" {
				reader, err := os.Open(configFile)
				if err != nil {
					return errors.Wrap(err, "error opening config file")
				}
				defer reader.Close()

				var configFromFile upgrade.Config
				const bufferSize = 512
				decoder := yaml.NewYAMLOrJSONDecoder(reader, bufferSize)
				if err := decoder.Decode(&configFromFile); err != nil {
					return errors.Wrap(err, "error parsing config file")
				}

				// Merge command line flags on top
				if err := mergo.Merge(&configFromFile, configFromFlags, mergo.WithOverride); err != nil {
					return errors.Wrap(err, "error merging flags with config file")
				}

				// Update our final config to be the merged copy
				finalConfig = configFromFile
			}

			upgrader, err := upgrade.NewControlPlaneUpgrader(newLogger(), finalConfig)
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
		&configFromFlags.ManagementCluster.Kubeconfig,
		"kubeconfig",
		"",
		"The kubeconfig path for the management cluster",
	)

	root.Flags().StringVar(
		&configFromFlags.TargetCluster.Namespace,
		"cluster-namespace",
		"",
		"The namespace of target cluster (required)",
	)

	root.Flags().StringVar(
		&configFromFlags.TargetCluster.Name,
		"cluster-name",
		"",
		"The name of target cluster (required)",
	)

	root.Flags().StringVar(
		&configFromFlags.KubernetesVersion,
		"kubernetes-version",
		"",
		"Desired kubernetes version to upgrade to (required)",
	)

	root.Flags().StringVar(
		&configFromFlags.UpgradeID,
		"upgrade-id",
		"",
		"Unique identifier used to resume a partial upgrade (required)",
	)

	root.Flags().StringVar(
		&configFromFlags.Patches.Infrastructure,
		"infrastructure-patches",
		"",
		"JSON patch expression of patches to apply to the machine's infrastructure resource (optional)",
	)

	root.Flags().StringVar(
		&configFromFlags.Patches.Bootstrap,
		"bootstrap-patches",
		"",
		"JSON patch expression of patches to apply to the machine's bootstrap resource (optional)",
	)

	root.Flags().DurationVar(
		&configFromFlags.MachineTimeout.Duration,
		"machine-timeout",
		configFromFlags.MachineTimeout.Duration,
		"How long to wait for machine operations (create, delete) to complete",
	)

	if err := root.Execute(); err != nil {
		// Print a stack trace, if possible. We may end up with the error message printed twice,
		// but the stack trace can be invaluable, so we'll accept this for the time being.
		fmt.Printf("%+v\n", err)
		os.Exit(1)
	}
}
