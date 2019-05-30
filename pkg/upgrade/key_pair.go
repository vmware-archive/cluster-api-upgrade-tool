// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"encoding/base64"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cluster-api-provider-aws/pkg/cloud/aws/services/certificates"
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

// KeyPair contains a cert and key.
type KeyPair struct {
	Cert []byte `json:"cert"`
	Key  []byte `json:"key"`
}

type keyPairGetter struct {
	secretClient corev1client.SecretsGetter
}

func newKeyPairGetter(secretClient corev1client.SecretsGetter) *keyPairGetter {
	return &keyPairGetter{
		secretClient: secretClient,
	}
}

func (k *keyPairGetter) getKeyPair(cluster *clusterapiv1alpha1.Cluster, config KeyPairConfig) (*KeyPair, error) {
	if config.ClusterField != "" {
		return getEmbeddedKeyPair(cluster, config.ClusterField)
	}

	return k.getSecretRefKeyPair(cluster.Namespace, config.SecretRef)
}

func getEmbeddedKeyPair(cluster *clusterapiv1alpha1.Cluster, path string) (*KeyPair, error) {
	logrus.Infof("getEmbeddedKeyPair, path=%s", path)
	pathParts := strings.Split(path, ".")
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

	return &KeyPair{
		Cert: cert,
		Key:  key,
	}, nil
}

func (k *keyPairGetter) getSecretRefKeyPair(secretNamespace, secretName string) (*KeyPair, error) {
	logrus.Infof("getSecretKeyPair, name=%s/%s", secretNamespace, secretName)
	secret, err := k.secretClient.Secrets(secretNamespace).Get(secretName, metav1.GetOptions{})
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

	return &KeyPair{
		Cert: cert,
		Key:  key,
	}, nil
}

func restConfigFromKeyPair(clusterName, url string, keyPair *KeyPair) (*rest.Config, error) {
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
