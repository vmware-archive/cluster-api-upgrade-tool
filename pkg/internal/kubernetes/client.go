// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (

	// We need this to enable authentication plugins. DO NOT REMOVE
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	bootstrapv1 "sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm/api/v1alpha2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KubeConfigPath represents the path to a kubeconfig file
type KubeConfigPath string

// KubeConfigContext represents a named context in a kubeconfig file
type KubeConfigContext string

// NewClient creates a new controller-runtime Client using the following priorities:
// 1) kubeconfig file,
// 2) $KUBECONFIG environment variable,
// 3) $HOME/.kube/config file
// kubeConfigContext is used if it is supplied.
func NewClient(kubeConfigPath KubeConfigPath, kubeConfigContext KubeConfigContext) (client.Client, error) {
	// The default loading rules will take $KUBECONFIG into account, if applicable.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = string(kubeConfigPath)

	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: string(kubeConfigContext),
	}

	clientcmdClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := clientcmdClientConfig.ClientConfig()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	scheme := runtime.NewScheme()
	if err := bootstrapv1.AddToScheme(scheme); err != nil {
		return nil, errors.Wrap(err, "error adding bootstrap api to scheme")
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		return nil, errors.Wrap(err, "error adding cluster api to scheme")
	}
	if err := v1.AddToScheme(scheme); err != nil {
		return nil, errors.Wrap(err, "error adding kubernetes api to scheme")
	}

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, errors.Wrap(err, "error creating controller runtime client")
	}

	return c, nil
}
