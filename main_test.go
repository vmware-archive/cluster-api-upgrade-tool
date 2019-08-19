package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/cluster-api/pkg/controller/remote"
)

// If you want to run just one at a time here are the commands:

// To test multiple control plane upgrades
// make test TESTCASE=TestMultipleControlPlaneUpgradeScenario

// To test a single control plane upgrade
// make test TESTCASE=TestUpgradeScenario

// To test machine deployment upgrade
// make test TESTCASE=TestMachineDeployment

// Code improvements:
// TODO: replace fmt.Println with t.Log
// TODO: Figure out a better naming strategy than hardcoding. I expected this to change when this test solidifies and then more tests are added
// TODO: CLEANUP CODE, probably use a defer. Not sure what kind of cleanup to do. Do we want to try to reuse the management cluster?
//       should each test get its own unique cluster and then have a single global teardown of the management cluster?
// TODO: Fixup the kubectl command to be nicer to use. It has a super weird signature right now. Suggested from review:
//       kubectl().ForCluster(c).WithReader(r).Run("apply", "-f", "-") might be nice!
// TODO: create a namespace. right now everything is default since that ns already exists.

const (
	workerNodeType        = "worker"
	controlPlaneNodeType  = "control-plane"
	capiImageName         = "us.gcr.io/k8s-artifacts-prod/cluster-api/cluster-api-controller:v0.1.6"
	capdImageName         = "gcr.io/kubernetes1-226021/capd-manager:latest"
	managementClusterName = "management"
	secretKubeconfigKey   = "value"
)

var (
	capdctlBinary = "hack/tools/bin/capdctl"
)

func init() {
	// Allow overriding the capdctl binary
	binary := os.Getenv("CAPDCTL")
	if binary != "" {
		capdctlBinary = binary
	}
}

func TestMultipleControlPlaneUpgradeScenario(t *testing.T) {
	// normal set up
	setupManagementCluster(t)

	// If these are not unique then you must tear down the management cluster or clean it thoroughly between runs
	clusterName := "my-cluster"
	namespace := "default"
	numberOfControlPlaneNodes := 3

	// Create a test cluster to upgrade
	if err := createTestControlPlane(clusterName, namespace, "v1.14.1", numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}
	if err := createWorkers(clusterName, namespace, "v1.14.1", 1); err != nil {
		t.Fatal(handleErr(err))
	}

	// wait for a secret to show up
	fmt.Println("Waiting up to 5 minutes for a the kubeconfig secret to appear")
	if err := waitForKubeconfigSecret(clusterName, namespace); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("Waiting for up to 5 minutes for etcd to be ready")
	if err := waitForEtcdReady(clusterName, namespace, numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("Starting the upgrade")
	path := kubeconfigPath("management")

	cfg := upgrade.Config{
		KubernetesVersion: "v1.14.2",
		ManagementCluster: upgrade.ManagementClusterConfig{
			Kubeconfig: string(path),
		},
		TargetCluster: upgrade.TargetClusterConfig{
			Name:         clusterName,
			Namespace:    namespace,
			UpgradeScope: upgrade.ControlPlaneScope,
			CAKeyPair: upgrade.KeyPairConfig{
				KubeconfigSecretRef: secretName(clusterName),
			},
		},
	}
	if err := upgradeCluster(cfg); err != nil {
		t.Fatal(handleErr(err))
	}

}

func TestMachineDeployment(t *testing.T) {
	setupManagementCluster(t)
	clusterName := "machine-deployment"
	namespace := "default"
	numberOfControlPlaneNodes := 1
	workerReplicas := 2

	if err := createTestControlPlane(clusterName, namespace, "v1.14.1", numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}
	if err := createMachineDeployment(clusterName, namespace, "v1.14.1", workerReplicas); err != nil {
		t.Fatal(handleErr(err))
	}

	// wait for kubeconfig to show up
	fmt.Println("Waiting up to 5 minutes for a the kubeconfig secret to appear")
	if err := waitForKubeconfigSecret(clusterName, namespace); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("Waiting for up to 5 minutes for etcd to be ready")
	if err := waitForEtcdReady(clusterName, namespace, numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}

	path := kubeconfigPath("management")
	cfg := upgrade.Config{
		KubernetesVersion: "v1.14.2",
		ManagementCluster: upgrade.ManagementClusterConfig{
			Kubeconfig: strings.TrimSpace(string(path)),
		},
		TargetCluster: upgrade.TargetClusterConfig{
			Name:         clusterName,
			Namespace:    namespace,
			UpgradeScope: upgrade.MachineDeploymentScope,
			CAKeyPair: upgrade.KeyPairConfig{
				KubeconfigSecretRef: secretName(clusterName),
			},
		},
	}

	fmt.Println("Waiting for the machines to be ready")
	if err := waitForNodesReady(clusterName, namespace, workerReplicas+numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("Starting the upgrade")
	if err := upgradeCluster(cfg); err != nil {
		t.Fatal(handleErr(err))
	}
}

func TestUpgradeScenario(t *testing.T) {
	// spin up management cluster
	setupManagementCluster(t)

	clusterName := "my-cluster"
	namespace := "default"
	numberOfControlPlaneNodes := 1

	// Create a test cluster to upgrade
	if err := createTestControlPlane(clusterName, namespace, "v1.14.1", numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}
	if err := createWorkers(clusterName, namespace, "v1.14.1", 1); err != nil {
		t.Fatal(handleErr(err))
	}

	// wait for kubeconfig to show up
	fmt.Println("Waiting up to 5 minutes for a the kubeconfig secret to appear")
	if err := waitForKubeconfigSecret(clusterName, namespace); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("Waiting for up to 5 minutes for etcd to be ready")
	if err := waitForEtcdReady(clusterName, namespace, numberOfControlPlaneNodes); err != nil {
		t.Fatal(handleErr(err))
	}

	fmt.Println("ready for testing!")
	// upgrade from 1.14.1 to 1.14.2

	// management cluster kubeconfig
	path := kubeconfigPath("management")

	cfg := upgrade.Config{
		KubernetesVersion: "v1.14.2",
		ManagementCluster: upgrade.ManagementClusterConfig{
			Kubeconfig: string(path),
		},
		TargetCluster: upgrade.TargetClusterConfig{
			Name:         clusterName,
			Namespace:    namespace,
			UpgradeScope: upgrade.ControlPlaneScope,
			CAKeyPair: upgrade.KeyPairConfig{
				KubeconfigSecretRef: secretName(clusterName),
			},
		},
	}
	if err := upgradeCluster(cfg); err != nil {
		t.Fatal(handleErr(err))
	}
}

func waitForKubeconfigSecret(clusterName, namespace string) error {
	return wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		_, err := kubectl(nil, managementClusterName, "get", "secret", "-n", namespace, secretName(clusterName))
		return err == nil, errors.WithStack(err)
	})
}

func waitForNodesReady(clusterName, namespace string, expected int) error {
	data, err := getChildKubeconfig(clusterName, namespace)
	if err != nil {
		return err
	}
	kubeconf, err := ioutil.TempFile("", "kubeconfig")
	if err != nil {
		return errors.WithStack(err)
	}
	defer os.Remove(kubeconf.Name())
	if err := ioutil.WriteFile(kubeconf.Name(), data, os.FileMode(0644)); err != nil {
		return errors.WithStack(err)
	}

	readyRe := regexp.MustCompile(`\s+Ready`)
	return wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		cmd := exec.Command("kubectl",
			"--kubeconfig", kubeconf.Name(),
			"get",
			"nodes",
			"--no-headers",
		)
		out, err := cmd.Output()
		if err != nil {
			return false, errors.WithStack(err)
		}
		lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
		idCount := 0
		for _, l := range lines {
			if readyRe.Match(l) {
				idCount++
			}
		}
		return idCount >= expected, nil
	})
}

func waitForProviderIDOnMachines(clusterName, namespace string, expected int) error {
	//k get machines -l cluster.k8s.io/cluster-name=my-cluster -o custom-columns=PROVIDERID:.spec.providerID --no-headers
	return wait.PollImmediate(5*time.Second, 10*time.Minute, func() (bool, error) {
		out, err := kubectl(nil, managementClusterName, "get", "machines",
			"--namespace", namespace,
			"--selector", fmt.Sprintf("cluster.k8s.io/cluster-name=%s", clusterName),
			"--output", "custom-columns=PROVIDERID:.spec.providerID",
			"--no-headers")
		if err != nil {
			return false, errors.WithStack(err)
		}

		lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
		idCount := 0
		for _, l := range lines {
			item := string(bytes.TrimSpace(l))
			if item != "<none>" {
				idCount++
			}
		}
		return idCount >= expected, nil
	})
}

func getChildKubeconfig(clusterName, namespace string) ([]byte, error) {
	secret, err := kubectl(nil, managementClusterName, "get", "secret", secretName(clusterName), "-n", namespace, "-o", fmt.Sprintf("jsonpath='{.data.%s}'", secretKubeconfigKey))
	if err != nil {
		return nil, err
	}
	secret = bytes.Trim(secret, "'")
	out := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
	n, err := base64.StdEncoding.Decode(out, secret)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return out[:n], nil
}

func waitForEtcdReady(clusterName, namespace string, expected int) error {
	data, err := getChildKubeconfig(clusterName, namespace)
	if err != nil {
		return err
	}
	kubeconf, err := ioutil.TempFile("", "kubeconfig")
	if err != nil {
		return errors.WithStack(err)
	}
	defer os.Remove(kubeconf.Name())
	if err := ioutil.WriteFile(kubeconf.Name(), data, os.FileMode(0644)); err != nil {
		return errors.WithStack(err)
	}
	return wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		cmd := exec.Command("kubectl",
			"--kubeconfig", kubeconf.Name(),
			"get",
			"po",
			"--namespace", "kube-system",
			"--selector", "component=etcd",
			"--output", "custom-columns=PHASE:.status.phase",
			"--no-headers",
		)
		out, err := cmd.Output()
		if err != nil {
			return false, errors.WithStack(err)
		}
		lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
		idCount := 0
		for _, l := range lines {
			item := string(bytes.TrimSpace(l))
			if len(item) == 0 {
				continue
			}
			if strings.Contains(item, "Running") {
				idCount++
			}
		}
		return idCount >= expected, nil
	})
}

func createTestControlPlane(clusterName, namespace, version string, numberOfControlPlanes int) error {
	if err := setupClusterObject(clusterName, namespace); err != nil {
		return err
	}
	for i := 0; i < numberOfControlPlanes; i++ {
		if err := createNode(clusterName, controlPlaneNodeType, namespace, version, i); err != nil {
			return err
		}
	}
	// wait for provider ID on all control planes before creating workerss
	fmt.Println("Waiting for up to 10 minutes for the provider IDs to appear on the machines")
	if err := waitForProviderIDOnMachines(clusterName, namespace, numberOfControlPlanes); err != nil {
		return err
	}
	return nil
}

func createWorkers(clusterName, namespace, version string, numberOfWorkers int) error {
	for i := 0; i < numberOfWorkers; i++ {
		if err := createNode(clusterName, workerNodeType, namespace, version, i); err != nil {
			return err
		}
	}
	return nil
}

func createMachineDeployment(clusterName, namespace, version string, replicas int) error {
	out, err := capdctl("machine-deployment",
		"--name", nodeName(clusterName, "machine-deployment", 0),
		"--namespace", namespace,
		"--cluster-name", clusterName,
		"--kubelet-version", version,
		"--replicas", fmt.Sprintf("%d", replicas))
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func setupClusterObject(clusterName, namespace string) error {
	out, err := capdctl("cluster", "--cluster-name", clusterName, "--namespace", namespace)
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func createNode(clusterName, nodeType, namespace, version string, count int) error {
	out, err := capdctl(nodeType,
		"--name", nodeName(clusterName, nodeType, count),
		"--namespace", namespace,
		"--cluster-name", clusterName,
		"--version", version)
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func nodeName(clusterName, kind string, number int) string {
	if number == 0 {
		return fmt.Sprintf("%s-%s", clusterName, kind)
	}
	return fmt.Sprintf("%s-%s%d", clusterName, kind, number)
}

// secretName generates the name of the kubeconfig secret stored on the management cluster
func secretName(clusterName string) string {
	return remote.KubeConfigSecretName(clusterName)
}

func handleErr(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return string(e.Stderr)
	}
	return err.Error()
}

func setupManagementCluster(t *testing.T) {
	t.Helper()
	if _, err := capdctl("setup", "-capd-image", capdImageName, "-capi-image", capiImageName, "-cluster-name", managementClusterName); err != nil {
		t.Fatal(handleErr(err))
	}
}

func pipeToKubectlApply(reader io.Reader) error {
	_, err := kubectl(reader, managementClusterName, "apply", "-f", "-")
	if err != nil {
		return err
	}
	return nil
}

func capdctl(args ...string) ([]byte, error) {
	cmd := exec.Command(capdctlBinary, args...)
	return cmd.Output()
}

// TODO this input is not right...
func kubectl(input io.Reader, cluster string, args ...string) ([]byte, error) {
	path := kubeconfigPath(cluster)
	args = append(args, "--kubeconfig", string(path))
	//fmt.Println("kubectl", args)
	cmd := exec.Command("kubectl", args...)
	if input != nil {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		go func() {
			// TODO: i guess we'll just ignore these errors
			defer stdin.Close()
			io.Copy(stdin, input)
		}()
	}
	return cmd.Output()
}

func kubeconfigPath(clusterName string) string {
	home := os.Getenv("HOME")
	return fmt.Sprintf("%s/.kube/kind-config-%s", home, clusterName)
}
