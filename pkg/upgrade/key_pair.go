// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"encoding/base64"
	"strings"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cluster-api-provider-aws/pkg/cloud/aws/services/certificates"
	clusterv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

// kubeconfigSecretKey is the key where the kubeconfig is stored in the secret.
const kubeconfigSecretKey = "kubeconfig"

// keyPair contains a cert and key.
type keyPair struct {
	Cert []byte `json:"cert"`
	Key  []byte `json:"key"`
}

// secrets has already been scoped to namespace, so, .Secrets("my-namespace") returns one of these.
type secrets interface {
	Get(name string, opts metav1.GetOptions) (*v1.Secret, error)
}

// NewRestConfigFromKubeconfigSecretRef decodes the kubeconfig stored in a secret and builds a *rest.Config with it.
func NewRestConfigFromKubeconfigSecretRef(secrets secrets, name string) (*rest.Config, error) {
	secret, err := secrets.Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// No need to decode from b64 since that's a kubectl thing
	kc, ok := secret.Data[kubeconfigSecretKey]
	if !ok {
		return nil, errors.Errorf("item 'kubeconfig' not found in secret %q", name)
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kc)
	return cfg, errors.WithStack(err)
}

// NewRestConfigFromCAClusterField returns a rest.Config configured with the CA key pair found in the cluster's
// object in the fieldpath specified. For example, "spec.providerSpec.value.caKeyPair" traverses the cluster
// object going through each '.' delimited field.
func NewRestConfigFromCAClusterField(cluster *clusterv1alpha1.Cluster, fieldPath, apiEndpoint string) (*rest.Config, error) {
	pathParts := strings.Split(fieldPath, ".")
	certPath := append(pathParts, "cert")
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
	if err != nil {
		return nil, errors.Wrap(err, "error converting cluster to unstructured")
	}
	certEncoded, found, err := unstructured.NestedString(u, certPath...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract key pair cert from %q", strings.Join(certPath, "."))
	}
	if !found {
		return nil, errors.Errorf("unable to find key pair cert in field %q", strings.Join(certPath, "."))
	}
	cert := make([]byte, base64.StdEncoding.DecodedLen(len(certEncoded)))
	if _, err := base64.StdEncoding.Decode(cert, []byte(certEncoded)); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair cert from secret")
	}

	keyPath := append(pathParts, "key")
	keyEncoded, found, err := unstructured.NestedString(u, keyPath...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract key pair key from %q", strings.Join(keyPath, "."))
	}
	if !found {
		return nil, errors.Errorf("unable to find key pair key in field %q", strings.Join(keyPath, "."))
	}
	key := make([]byte, base64.StdEncoding.DecodedLen(len(keyEncoded)))
	if _, err = base64.StdEncoding.Decode(key, []byte(keyEncoded)); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair key from secret")
	}
	kp := &keyPair{
		Cert: cert,
		Key:  key,
	}
	return restConfigFromKeyPair(cluster.GetName(), apiEndpoint, kp)
}

// NewRestConfigFromCASecretRef gets the CA key pair from the secret and builds a *rest.Config with them.
func NewRestConfigFromCASecretRef(secretClient secrets, name, clusterName, apiEndpoint string) (*rest.Config, error) {
	secret, err := secretClient.Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving key pair secret ref")
	}

	certEncoded, ok := secret.Data["cert"]
	if !ok {
		return nil, errors.New("unable to find key pair cert in secret")
	}
	cert := make([]byte, base64.StdEncoding.DecodedLen(len(certEncoded)))
	if _, err := base64.StdEncoding.Decode(cert, certEncoded); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair cert from secret")
	}

	keyEncoded, ok := secret.Data["key"]
	if !ok {
		return nil, errors.New("unable to find key pair key in secret")
	}
	key := make([]byte, base64.StdEncoding.DecodedLen(len(keyEncoded)))
	if _, err = base64.StdEncoding.Decode(key, keyEncoded); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair key from secret")
	}

	kp := &keyPair{
		Cert: cert,
		Key:  key,
	}
	return restConfigFromKeyPair(clusterName, apiEndpoint, kp)
}

func restConfigFromKeyPair(clusterName, url string, keyPair *keyPair) (*rest.Config, error) {
	// Borrowed from CAPA
	cert, err := certificates.DecodeCertPEM(keyPair.Cert)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode CA Cert")
	} else if cert == nil {
		return nil, errors.New("certificate not found in config")
	}

	key, err := certificates.DecodePrivateKeyPEM(keyPair.Key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode private key")
	} else if key == nil {
		return nil, errors.New("key not found in status")
	}

	cfg, err := certificates.NewKubeconfig(clusterName, url, cert, key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate a kubeconfig")
	}
	// End borrowed

	// TODO see if we can avoid going to yaml and back
	kubeConfigYaml, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize a kubeconfig")
	}

	// targetRestConfig, err := common.NewRestConfig(config.TargetCluster.Kubeconfig, "")
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYaml)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get a rest config from kubeconfig")
	}

	return restConfig, nil
}
