package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
)

// Code improvements:
// TODO: replace fmt.Println with t.Log
// TODO: instead of panicking fatal the test case
// TODO: Look into wait.PollImmediate instead of this home-grown solution, dep might not be worth it though, depends on how much code it is
// TODO: Figure out a better naming strategy than hardcoding. I expected this to change when this test solidifies and then more tests are added
// TODO: CLEANUP CODE, probably use a defer. Not sure what kind of cleanup to do. Do we want to try to reuse the management cluster?
//       should each test get its own unique cluster and then have a single global teardown of the management cluster?
// TODO: Fixup the kubectl command to be nicer to use. It has a super weird signature right now. Suggested from review:
//       kubectl().ForCluster(c).WithReader(r).Run("apply", "-f", "-") might be nice!

func TestMultipleControlPlaneUpgradeScenario(t *testing.T) {
	// normal set up
	setupManagementCluster(t)

	// If these are not unique then you must tear down the management cluster or clean it thoroughly between runs
	clusterName := "my-cluster"
	namespace := "default"
	numberOfControlPlaneNodes := 3

	// Create a test cluster to upgrade
	createTestCluster(t, clusterName, namespace, "v1.14.1", numberOfControlPlaneNodes)

	// wait for a secret to show up
	fmt.Println("Waiting up to 5 minutes for a the kubeconfig secret to appear")
	if err := waitForKubeconfigSecret(clusterName, namespace); err != nil {
		t.Fatal(err)
	}

	fmt.Println("Waiting for up to 5 minutes for etcd to be ready")
	if err := waitForEtcdReady(clusterName, namespace, numberOfControlPlaneNodes); err != nil {
		t.Fatalf("%+v\n", err)
	}

	fmt.Println("Starting the upgrade")
	path, err := kind("get", "kubeconfig-path", "--name", "kind")
	if err != nil {
		t.Fatal(err)
	}

	path = bytes.TrimSpace(path)
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
		t.Fatalf("%+v", err)
	}
}

func TestUpgradeScenario(t *testing.T) {
	// spin up management cluster
	setupManagementCluster(t)

	clusterName := "my-cluster"
	namespace := "default"
	numberOfControlPlaneNodes := 1

	// Create a test cluster to upgrade
	createTestCluster(t, clusterName, namespace, "v1.14.1", numberOfControlPlaneNodes)

	// wait for kubeconfig to show up
	fmt.Println("Waiting up to 5 minutes for a the kubeconfig secret to appear")
	if err := waitForKubeconfigSecret(clusterName, namespace); err != nil {
		panic(err)
	}

	fmt.Println("Waiting for up to 5 minutes for etcd to be ready")
	if err := waitForEtcdReady(clusterName, namespace, numberOfControlPlaneNodes); err != nil {
		panic(fmt.Sprintf("%+v\n", err))
	}

	fmt.Println("ready for testing!")
	// upgrade from 1.14.1 to 1.14.2

	// management cluster kubeconfig
	path, err := kind("get", "kubeconfig-path", "--name", "kind")
	if err != nil {
		panic(err)
	}

	path = bytes.TrimSpace(path)
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
		t.Fatalf("%+v", err)
	}
}

func waitForKubeconfigSecret(clusterName, namespace string) error {
	timer := time.NewTimer(5 * time.Minute)
	ticker := time.Tick(10 * time.Second)
	for {
		select {
		case <-timer.C:
			return errors.New("timed out waiting for kubeconfig to appear")
		case <-ticker:
			_, err := kubectl(nil, "kind", "get", "secret", "-n", namespace, secretName(clusterName))
			if err != nil {
				continue
			}
			return nil
		}
	}
}

func waitForProviderIDOnMachines(clusterName, namespace string, expected int) error {
	// get all machines
	//k get machines -l cluster.k8s.io/cluster-name=my-cluster -o custom-columns=PROVIDERID:.spec.providerID --no-headers
	timeout := time.NewTimer(10 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-timeout.C:
			return errors.New("timed out waiting for provider IDs")
		case <-ticker.C:
			out, err := kubectl(nil, "kind", "get", "machines",
				"--namespace", namespace,
				"--selector", fmt.Sprintf("cluster.k8s.io/cluster-name=%s", clusterName),
				"--output", "custom-columns=PROVIDERID:.spec.providerID",
				"--no-headers")
			if err != nil {
				return errors.WithStack(err)
			}

			lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			idCount := 0
			for _, l := range lines {
				item := string(bytes.TrimSpace(l))
				if item != "<none>" {
					idCount++
				}
			}
			if idCount >= expected {
				return nil
			}
		}
	}
	// inspect provider iD
	//cluster.k8s.io/cluster-name: my-cluster
}

func waitForEtcdReady(clusterName, namespace string, expected int) error {
	secret, err := kubectl(nil, "kind", "get", "secret", secretName(clusterName), "-n", namespace, "-o", "jsonpath='{.data.kubeconfig}'")
	if err != nil {
		return err
	}
	secret = bytes.Trim(secret, "'")
	out := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
	n, err := base64.StdEncoding.Decode(out, secret)
	if err != nil {
		return errors.WithStack(err)
	}
	out = out[:n]
	kubeconf, err := ioutil.TempFile("", "kubeconfig")
	if err != nil {
		return errors.WithStack(err)
	}
	defer os.Remove(kubeconf.Name())
	if err := ioutil.WriteFile(kubeconf.Name(), out, os.FileMode(0644)); err != nil {
		return errors.WithStack(err)
	}
	timeout := time.NewTimer(5 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-timeout.C:
			return errors.New("timed out waiting for cluster to become ready")
		case <-ticker.C:
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
				return errors.WithStack(err)
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
			if idCount >= expected {
				return nil
			}
		}
	}
}

func createTestCluster(t *testing.T, clusterName, namespace, version string, numberOfControlPlanes int) {
	t.Helper()
	if err := setupClusterObject(clusterName, namespace); err != nil {
		t.Fatal(handleErr(err))
	}
	for i := 0; i < numberOfControlPlanes; i++ {
		if err := setupControlPlaneObject(clusterName, namespace, version, i); err != nil {
			t.Fatal(handleErr(err))
		}
	}
	// wait for provider ID on all control planes before creating workerss
	fmt.Println("Waiting for up to 10 minutes for the provider IDs to appear on the machines")
	if err := waitForProviderIDOnMachines(clusterName, namespace, numberOfControlPlanes); err != nil {
		t.Fatal(err)
	}
	if err := setupWorkerObject(clusterName, namespace, version); err != nil {
		t.Fatal(handleErr(err))
	}
}

func setupClusterObject(clusterName, namespace string) error {
	out, err := capdctl("cluster", "--cluster-name", clusterName, "--namespace", namespace)
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func setupControlPlaneObject(clusterName, namespace, version string, count int) error {
	out, err := capdctl(
		"control-plane",
		"--name", controlPlaneName(clusterName, count),
		"--namespace", namespace,
		"--cluster-name", clusterName,
		"--version", version)
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func setupWorkerObject(clusterName, namespace, version string) error {
	out, err := capdctl("worker",
		"--name", workerName(clusterName),
		"--namespace", namespace,
		"--cluster-name", clusterName,
		"--version", version)
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

// controlPlaneName generates the base control-plane node name for a given cluster
func controlPlaneName(clusterName string, number int) string {
	if number == 0 {
		return fmt.Sprintf("%s-control-plane", clusterName)
	}
	return fmt.Sprintf("%s-control-plane%d", clusterName, number)
}

// workerName generates the base worker node name for a given cluster
func workerName(clusterName string) string {
	return fmt.Sprintf("%s-worker", clusterName)
}

// secretName generates the name of the kubeconfig secret stored on the management cluster
func secretName(clusterName string) string {
	return fmt.Sprintf("kubeconfig-%s", clusterName)
}

func handleErr(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return string(e.Stderr)
	}
	return err.Error()
}

func setupManagementCluster(t *testing.T) {
	t.Helper()
	// if a cluster named kind already exists then we assume we're good to go:
	// TODO: this isn't very good because we have to clean up the management cluster or use unique machines etc.
	clusters, err := kind("get", "clusters")
	if err != nil {
		panic(err)
	}
	for _, cluster := range bytes.Split(clusters, []byte("\n")) {
		if string(bytes.TrimSpace(cluster)) == "kind" {
			fmt.Println("Detected a management cluster.")
			return
		}
	}

	if _, err := capdctl("setup"); err != nil {
		t.Fatal(handleErr(err))
	}
	if err := installCRDs(); err != nil {
		t.Fatal(handleErr(err))
	}
	if err := installCAPD(); err != nil {
		t.Fatal(handleErr(err))
	}
}

func pipeToKubectlApply(reader io.Reader) error {
	_, err := kubectl(reader, "kind", "apply", "-f", "-")
	if err != nil {
		return err
	}
	return nil
}

func installCAPD() error {
	out, err := capdctl("capd")
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func installCRDs() error {
	out, err := capdctl("crds")
	if err != nil {
		return err
	}
	return pipeToKubectlApply(bytes.NewReader(out))
}

func capdctl(args ...string) ([]byte, error) {
	cmd := exec.Command("capdctl", args...)
	return cmd.Output()
}

// TODO this input is not right...
func kubectl(input io.Reader, cluster string, args ...string) ([]byte, error) {
	// get environment from kind
	path, err := kind("get", "kubeconfig-path", "--name", cluster)
	if err != nil {
		return nil, err
	}
	path = bytes.TrimSpace(path)
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

func kind(args ...string) ([]byte, error) {
	cmd := exec.Command("kind", args...)
	return cmd.Output()
}
