// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"github.com/pkg/errors"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewRestConfig creates a *rest.Config using the following priorities:
// 1) kubeconfig file,
// 2) $KUBECONFIG environment variable,
// 3) $HOME/.kube/config file
// Context is used if it is supplied.
func NewRestConfig(kubeconfig string, context string) (*rest.Config, error) {
	// The default loading rules will take $KUBECONFIG into account, if applicable.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig

	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: context,
	}

	clientcmdClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := clientcmdClientConfig.ClientConfig()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return config, nil
}
