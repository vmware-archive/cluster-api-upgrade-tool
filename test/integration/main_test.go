package integration

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/logging"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	cabpkv1 "sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm/api/v1alpha2"
	"sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm/kubeadm/v1beta1"
	capdv1 "sigs.k8s.io/cluster-api-provider-docker/api/v1alpha2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/restmapper"
	"sigs.k8s.io/cluster-api/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgrade(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgrade Suite")
}

const kindConfig = `kind: Cluster
apiVersion: kind.sigs.k8s.io/v1alpha3
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: /var/run/docker.sock
        containerPath: /var/run/docker.sock`

var _ = Describe("Upgrade Tool", func() {
	var (
		managementClusterClient client.Client
		managementClusterName   string
		clusterNS               string
		cluster                 *clusterv1.Cluster
		ctx                     = context.Background()
	)

	BeforeEach(func() {
		var err error
		managementClusterName = fmt.Sprintf("mgmt-%d", time.Now().Unix())

		tempKindConfigFile, err := ioutil.TempFile("", "")
		Expect(err).To(BeNil())
		defer os.Remove(tempKindConfigFile.Name())

		_, err = fmt.Fprintln(tempKindConfigFile, kindConfig)
		Expect(err).To(BeNil())

		// normal set up
		By("Creating a management cluster")
		Expect(runCommand("kind", "create", "cluster", "--name", managementClusterName, "--config", tempKindConfigFile.Name())).To(Succeed())

		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		loadingRules.ExplicitPath = kubeconfigPath(managementClusterName)
		clientcmdClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

		config, err := clientcmdClientConfig.ClientConfig()
		Expect(err).To(BeNil())

		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		Expect(clusterv1.AddToScheme(scheme)).To(Succeed())
		Expect(cabpkv1.AddToScheme(scheme)).To(Succeed())
		Expect(capdv1.AddToScheme(scheme)).To(Succeed())

		restMapper, err := restmapper.NewCached(config)
		Expect(err).To(BeNil())

		By("Creating a management cluster client")
		managementClusterClient, err = client.New(config, client.Options{Scheme: scheme, Mapper: restMapper})
		Expect(err).To(BeNil())

		yamls := []string{
			"https://github.com/kubernetes-sigs/cluster-api/releases/download/v0.2.6/cluster-api-components.yaml",
			"https://github.com/kubernetes-sigs/cluster-api-bootstrap-provider-kubeadm/releases/download/v0.1.4/bootstrap-components.yaml",
			"https://github.com/kubernetes-sigs/cluster-api-provider-docker/releases/download/v0.2.0/provider_components.yaml",
		}

		var crds []string

		By("Deploying CAPI, CABPK, CAPD components")
		for _, y := range yamls {
			// Use an anonymous func so defer calls happen early
			func() {
				resp, err := http.Get(y)
				Expect(err).To(BeNil())
				defer resp.Body.Close()
				decoder := yaml.NewYAMLDecoder(resp.Body)
				for {
					var u unstructured.Unstructured
					_, gvk, err := decoder.Decode(nil, &u)
					if err == io.EOF {
						break
					}
					Expect(err).To(BeNil())

					Expect(managementClusterClient.Create(ctx, &u)).To(Succeed())

					if gvk.Kind == "CustomResourceDefinition" {
						crds = append(crds, u.GetName())
					}
				}
			}()
		}

		By("Waiting for CRDs to be ready")
		err = wait.PollImmediate(time.Second, time.Minute, func() (bool, error) {
			By("Inside PollImmediate")
			for _, crd := range crds {
				var u unstructured.Unstructured
				u.SetAPIVersion("apiextensions.k8s.io/v1beta1")
				u.SetKind("CustomResourceDefinition")

				By("Getting CRD " + crd)
				Expect(managementClusterClient.Get(ctx, client.ObjectKey{Name: crd}, &u)).To(Succeed())

				By("Checking its conditions")
				conditions, _, err := unstructured.NestedSlice(u.Object, "status", "conditions")
				Expect(err).To(BeNil())
				established := false
				accepted := false
				for _, condition := range conditions {
					m, ok := condition.(map[string]interface{})
					Expect(ok).To(BeTrue())
					switch m["type"].(string) {
					case "Established":
						established = true
					case "NamesAccepted":
						accepted = true
					}
				}
				if !accepted || !established {
					return false, nil
				}
			}
			return true, nil
		})
		Expect(err).To(BeNil())

		By("Creating a test namespace")
		ns := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "upgrade-",
			},
		}
		Expect(managementClusterClient.Create(ctx, &ns)).To(Succeed())
		// Get the generated name
		clusterNS = ns.Name
	})

	AfterEach(func() {
		By("Deleting a management cluster")
		Expect(runCommand("kind", "delete", "cluster", "--name", managementClusterName)).To(Succeed())
	})

	Describe("Control plane upgrades", func() {
		It("Should work", func() {
			By("Creating a DockerCluster")
			dockerCluster := capdv1.DockerCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    clusterNS,
					GenerateName: "upgrade-",
				},
			}

			Expect(managementClusterClient.Create(ctx, &dockerCluster)).To(Succeed())

			By("Creating a Cluster")
			cluster = &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    clusterNS,
					GenerateName: "upgrade-",
				},
				Spec: clusterv1.ClusterSpec{
					ClusterNetwork: &clusterv1.ClusterNetwork{
						Pods: &clusterv1.NetworkRanges{
							CIDRBlocks: []string{"192.168.0.0/16"},
						},
					},
					InfrastructureRef: &v1.ObjectReference{
						APIVersion: capdv1.GroupVersion.String(),
						Kind:       "DockerCluster",
						Namespace:  clusterNS,
						Name:       dockerCluster.Name,
					},
				},
			}
			Expect(managementClusterClient.Create(ctx, cluster)).To(Succeed())

			By("Creating a DockerMachine")
			dockerMachine := capdv1.DockerMachine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    clusterNS,
					GenerateName: "upgrade-",
				},
			}
			Expect(managementClusterClient.Create(ctx, &dockerMachine)).To(Succeed())

			By("Creating a KubeadmConfig")
			kubeadmConfig := cabpkv1.KubeadmConfig{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    clusterNS,
					GenerateName: "upgrade-",
				},
				Spec: cabpkv1.KubeadmConfigSpec{
					InitConfiguration: &v1beta1.InitConfiguration{
						NodeRegistration: v1beta1.NodeRegistrationOptions{
							KubeletExtraArgs: map[string]string{
								"eviction-hard": "nodefs.available<0%,nodefs.inodesFree<0%,imagefs.available<0%",
							},
						},
					},
					ClusterConfiguration: &v1beta1.ClusterConfiguration{
						ControllerManager: v1beta1.ControlPlaneComponent{
							ExtraArgs: map[string]string{
								"enable-hostpath-provisioner": "true",
							},
						},
					},
				},
			}
			Expect(managementClusterClient.Create(ctx, &kubeadmConfig)).To(Succeed())

			By("Creating a Machine")
			machine := clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    clusterNS,
					GenerateName: "upgrade-",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name":  cluster.Name,
						"cluster.x-k8s.io/control-plane": "true",
					},
				},
				Spec: clusterv1.MachineSpec{
					Version: pointer.StringPtr("v1.14.1"),
					Bootstrap: clusterv1.Bootstrap{
						ConfigRef: &v1.ObjectReference{
							APIVersion: cabpkv1.GroupVersion.String(),
							Kind:       "KubeadmConfig",
							Namespace:  clusterNS,
							Name:       kubeadmConfig.Name,
						},
					},
					InfrastructureRef: v1.ObjectReference{
						APIVersion: capdv1.GroupVersion.String(),
						Kind:       "DockerMachine",
						Namespace:  clusterNS,
						Name:       dockerMachine.Name,
					},
				},
			}
			Expect(managementClusterClient.Create(ctx, &machine)).To(Succeed())

			// wait for a secret to show up
			By("Waiting up to 5 minutes for a the kubeconfig secret to appear")
			Expect(wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
				_, err := kubeconfig.FromSecret(managementClusterClient, cluster)
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return err == nil, err
			})).To(Succeed())

			By("Waiting for the control plane machine to have a node ref")
			Expect(wait.PollImmediate(5*time.Second, 10*time.Minute, func() (bool, error) {
				var m clusterv1.Machine
				mKey := client.ObjectKey{Namespace: machine.Namespace, Name: machine.Name}
				if err := managementClusterClient.Get(ctx, mKey, &m); err != nil {
					return false, err
				}
				return m.Status.NodeRef != nil, nil
			})).To(Succeed())

			By("Waiting for up to 5 minutes for etcd to be ready")
			workloadClient, err := workloadClusterClient(managementClusterClient, cluster)
			Expect(err).To(BeNil())

			expectedEtcdPods := 1
			Expect(wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
				var podList v1.PodList
				listOptions := []client.ListOption{
					client.InNamespace("kube-system"),
					client.MatchingLabels{"component": "etcd"},
				}
				if err := workloadClient.List(ctx, &podList, listOptions...); err != nil {
					return false, err
				}

				actual := 0
				for _, pod := range podList.Items {
					if pod.Status.Phase == v1.PodRunning {
						actual++
					}
				}

				return actual >= expectedEtcdPods, nil
			})).To(Succeed())

			By("Performing the upgrade")
			path := kubeconfigPath(managementClusterName)

			cfg := upgrade.Config{
				KubernetesVersion: "v1.14.2",
				ManagementCluster: upgrade.ManagementClusterConfig{
					Kubeconfig: string(path),
				},
				TargetCluster: upgrade.TargetClusterConfig{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				},
				UpgradeID: "1",
			}

			log := logrus.New()
			log.Out = os.Stdout
			upgradeLog := logging.NewLogrusLoggerAdapter(log)

			upgrader, err := upgrade.NewControlPlaneUpgrader(upgradeLog, cfg)
			Expect(err).To(BeNil())

			Expect(upgrader.Upgrade()).To(Succeed())

			// TODO check that we have a single upgraded machine
			// TODO check that we have a single upgraded node
			// TODO check that the secret ownerrefs have been updated accurately
		})
	})
})

func workloadClusterClient(managementClusterClient client.Client, cluster *clusterv1.Cluster) (client.Client, error) {
	kubeConfig, err := kubeconfig.FromSecret(managementClusterClient, cluster)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve kubeconfig secret for Cluster %s/%s", cluster.Namespace, cluster.Name)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create client configuration for Cluster %s/%s", cluster.Namespace, cluster.Name)
	}

	ret, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create client for Cluster %s/%s", cluster.Namespace, cluster.Name)
	}

	return ret, nil
}

func runCommand(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	out, err := cmd.Output()
	fmt.Fprintln(GinkgoWriter, string(out))
	return err
}

func kubeconfigPath(clusterName string) string {
	home := os.Getenv("HOME")
	return fmt.Sprintf("%s/.kube/kind-config-%s", home, clusterName)
}
