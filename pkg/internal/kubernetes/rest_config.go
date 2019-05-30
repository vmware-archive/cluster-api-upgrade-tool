// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func NewRestConfig(kubeconfig string, context string) (*rest.Config, error) {
	// The default loading rules will take $KUBECONFIG into account, if applicable.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig

	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: context,
	}

	clientcmdClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	// We probably want to fail gracefully here and not panic....
	config, err := clientcmdClientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	return config, nil
}
