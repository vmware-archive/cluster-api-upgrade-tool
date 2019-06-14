// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package secrets provides a variety of ways to get a config that can connect to a kubernetes cluster.
package upgrade

import (
	"crypto/rsa"
	"crypto/x509"
	"strings"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/cluster-api-provider-aws/pkg/cloud/aws/services/certificates" // RIP
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

type secretsClient interface {
	Get(string, metav1.GetOptions) (*v1.Secret, error)
}
type decoder interface {
	Decode([]byte, []byte) (int, error)
	DecodedLen(n int) int
}
type kubeconfigClient interface {
	Write(clientcmdapi.Config) ([]byte, error)
	RESTConfigFromKubeConfig([]byte) (*restclient.Config, error)
	NewKubeconfig(string, string, *x509.Certificate, *rsa.PrivateKey) (*clientcmdapi.Config, error)
	DecodeCertPEM([]byte) (*x509.Certificate, error)
	DecodePrivateKeyPEM([]byte) (*rsa.PrivateKey, error)
}

type kubeconfig struct{}

func (k *kubeconfig) NewKubeconfig(a, b string, c *x509.Certificate, d *rsa.PrivateKey) (*clientcmdapi.Config, error) {
	return certificates.NewKubeconfig(a, b, c, d)
}
func (k *kubeconfig) RESTConfigFromKubeConfig(a []byte) (*restclient.Config, error) {
	return clientcmd.RESTConfigFromKubeConfig(a)
}
func (k *kubeconfig) Write(a clientcmdapi.Config) ([]byte, error) {
	return clientcmd.Write(a)
}
func (k *kubeconfig) DecodeCertPEM(a []byte) (*x509.Certificate, error) {
	return certificates.DecodeCertPEM(a)
}
func (k *kubeconfig) DecodePrivateKeyPEM(a []byte) (*rsa.PrivateKey, error) {
	return certificates.DecodePrivateKeyPEM(a)
}

type unstructuredFunctions interface {
	ToUnstructured(interface{}) (map[string]interface{}, error)
	NestedString(map[string]interface{}, ...string) (string, bool, error)
}
type unstructuredClient struct{}

func (u *unstructuredClient) ToUnstructured(i interface{}) (map[string]interface{}, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(i)
}
func (u *unstructuredClient) NestedString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	return unstructured.NestedString(obj, fields...)
}

// keyPair contains a cert and key.
type keyPair struct {
	Cert []byte `json:"cert"`
	Key  []byte `json:"key"`
}

type keyPairGetter struct {
	secretsClient      secretsClient
	decoder            decoder
	kubeconfigClient   kubeconfigClient
	unstructuredClient unstructuredFunctions
}

// GetRestConfig figures out how to build a rest.Config from one of three possible ways based on the KeyPairConfig passed in.
func GetRestConfig(secretsClient secretsClient, decoder decoder, kubeconfigClient kubeconfigClient, cluster *clusterapiv1alpha1.Cluster, targetClusterAPIEndpoint string, config KeyPairConfig) (*rest.Config, error) {
	kpg := newKeyPairGetter(secretsClient, decoder, kubeconfigClient)
	if config.KubeconfigSecretRef == "" {
		kp, err := kpg.getKeyPair(cluster, config)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		return kpg.restConfigFromKeyPair(cluster.GetName(), targetClusterAPIEndpoint, kp)
	}

	secret, err := secretsClient.Get(config.KubeconfigSecretRef, metav1.GetOptions{})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	kc, ok := secret.Data["kubeconfig"]
	if !ok {
		return nil, errors.Errorf("item 'kubeconfig' not found in secret %q in namespace %q", config.KubeconfigSecretRef, cluster.GetNamespace())
	}
	var out []byte
	if _, err := decoder.Decode(kc, out); err != nil {
		return nil, errors.WithStack(err)
	}
	return kpg.kubeconfigClient.RESTConfigFromKubeConfig(out)
}

func newKeyPairGetter(secretsClient secretsClient, decoder decoder, kubeconfigClient kubeconfigClient) *keyPairGetter {
	return &keyPairGetter{
		secretsClient:      secretsClient,
		decoder:            decoder,
		kubeconfigClient:   kubeconfigClient,
		unstructuredClient: &unstructuredClient{},
	}
}

func (k *keyPairGetter) getKeyPair(cluster *clusterapiv1alpha1.Cluster, config KeyPairConfig) (*keyPair, error) {
	if config.ClusterField != "" {
		return k.getEmbeddedKeyPair(cluster, config.ClusterField)
	}

	return k.getSecretRefKeyPair(config.SecretRef)
}

func (k *keyPairGetter) getEmbeddedKeyPair(cluster *clusterapiv1alpha1.Cluster, path string) (*keyPair, error) {
	pathParts := strings.Split(path, ".")
	certPath := append(pathParts, "cert")
	u, err := k.unstructuredClient.ToUnstructured(cluster)
	if err != nil {
		return nil, errors.Wrap(err, "error converting cluster to unstructured")
	}
	certEncoded, found, err := k.unstructuredClient.NestedString(u, certPath...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract key pair cert from %q", strings.Join(certPath, "."))
	}
	if !found {
		return nil, errors.Errorf("unable to find key pair cert in field %q", strings.Join(certPath, "."))
	}
	cert := make([]byte, k.decoder.DecodedLen(len(certEncoded)))
	if _, err := k.decoder.Decode(cert, []byte(certEncoded)); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair cert from secret")
	}

	keyPath := append(pathParts, "key")
	keyEncoded, found, err := k.unstructuredClient.NestedString(u, keyPath...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract key pair key from %q", strings.Join(keyPath, "."))
	}
	if !found {
		return nil, errors.Errorf("unable to find key pair key in field %q", strings.Join(keyPath, "."))
	}
	key := make([]byte, k.decoder.DecodedLen(len(keyEncoded)))
	if _, err = k.decoder.Decode(key, []byte(keyEncoded)); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair key from secret")
	}

	return &keyPair{
		Cert: cert,
		Key:  key,
	}, nil
}

func (k *keyPairGetter) getSecretRefKeyPair(secretName string) (*keyPair, error) {
	secret, err := k.secretsClient.Get(secretName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving key pair secret ref")
	}

	certEncoded, ok := secret.Data["cert"]
	if !ok {
		return nil, errors.New("unable to find key pair cert in secret")
	}
	cert := make([]byte, k.decoder.DecodedLen(len(certEncoded)))
	if _, err := k.decoder.Decode(cert, certEncoded); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair cert from secret")
	}

	keyEncoded, ok := secret.Data["key"]
	if !ok {
		return nil, errors.New("unable to find key pair key in secret")
	}
	key := make([]byte, k.decoder.DecodedLen(len(keyEncoded)))
	if _, err = k.decoder.Decode(key, keyEncoded); err != nil {
		return nil, errors.Wrap(err, "error decoding key pair key from secret")
	}

	return &keyPair{
		Cert: cert,
		Key:  key,
	}, nil
}

func (k *keyPairGetter) restConfigFromKeyPair(clusterName, url string, keyPair *keyPair) (*rest.Config, error) {
	// Borrowed from CAPA
	cert, err := k.kubeconfigClient.DecodeCertPEM(keyPair.Cert)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode CA Cert")
	} else if cert == nil {
		return nil, errors.New("certificate not found in config")
	}

	key, err := k.kubeconfigClient.DecodePrivateKeyPEM(keyPair.Key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode private key")
	} else if key == nil {
		return nil, errors.New("key not found in status")
	}

	cfg, err := k.kubeconfigClient.NewKubeconfig(clusterName, url, cert, key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate a kubeconfig")
	}
	// End borrowed

	// TODO see if we can avoid going to yaml and back
	kubeConfigYaml, err := k.kubeconfigClient.Write(*cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize a kubeconfig")
	}

	// targetRestConfig, err := common.NewRestConfig(config.TargetCluster.Kubeconfig, "")
	restConfig, err := k.kubeconfigClient.RESTConfigFromKubeConfig(kubeConfigYaml)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get a rest config from kubeconfig")
	}

	return restConfig, nil
}
