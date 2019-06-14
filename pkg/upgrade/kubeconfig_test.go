package upgrade_test

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

type secretsclient struct {
	secret *v1.Secret
}

func (s *secretsclient) Get(name string, opts metav1.GetOptions) (*v1.Secret, error) {
	return s.secret, nil
}

type kubeconfig struct {
	certPEM      *x509.Certificate
	privateKey   *rsa.PrivateKey
	clientConfig *clientcmdapi.Config
	restConfig   *restclient.Config
}

func (k *kubeconfig) Write(clientcmdapi.Config) ([]byte, error) {
	return nil, nil
}
func (k *kubeconfig) RESTConfigFromKubeConfig([]byte) (*restclient.Config, error) {
	return k.restConfig, nil
}
func (k *kubeconfig) NewKubeconfig(string, string, *x509.Certificate, *rsa.PrivateKey) (*clientcmdapi.Config, error) {
	return k.clientConfig, nil
}
func (k *kubeconfig) DecodeCertPEM([]byte) (*x509.Certificate, error) {
	return k.certPEM, nil
}
func (k *kubeconfig) DecodePrivateKeyPEM([]byte) (*rsa.PrivateKey, error) {
	return k.privateKey, nil
}

func TestGetRestConfig(t *testing.T) {
	testcases := []struct {
		name           string
		secretContents map[string][]byte
		cert           x509.Certificate
		key            rsa.PrivateKey
		clientConfig   clientcmdapi.Config
		restConfig     restclient.Config
	}{
		{
			name: "simple",
			secretContents: map[string][]byte{
				"cert": []byte(""),
				"key":  []byte(""),
			},
			cert:         x509.Certificate{},
			key:          rsa.PrivateKey{},
			clientConfig: clientcmdapi.Config{},
			restConfig:   restclient.Config{},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &secretsclient{
				secret: &v1.Secret{
					Data: tc.secretContents,
				},
			}
			kc := &kubeconfig{
				certPEM:      &tc.cert,
				privateKey:   &tc.key,
				clientConfig: &tc.clientConfig,
				restConfig:   &tc.restConfig,
			}
			cluster := v1alpha1.Cluster{}
			rc, err := upgrade.GetRestConfig(sc, base64.StdEncoding, kc, &cluster, "", upgrade.KeyPairConfig{})
			if err != nil {
				t.Fatalf("%+v", err)
			}
			if rc == nil {
				t.Fatal("restconfig is nil but it should not be")
			}
		})
	}
}
