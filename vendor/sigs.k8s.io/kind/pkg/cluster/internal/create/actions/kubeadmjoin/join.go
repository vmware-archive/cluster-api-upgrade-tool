/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package kubeadmjoin implements the kubeadm config action
package kubeadmjoin

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/internal/kubeadm"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/fs"
)

// Action implements action for creating the kubeadm config
// and deployng it on the bootrap control-plane node.
type Action struct{}

// NewAction returns a new action for creating the kubadm config
func NewAction() actions.Action {
	return &Action{}
}

// Execute runs the action
func (a *Action) Execute(ctx *actions.ActionContext) error {
	allNodes, err := ctx.Nodes()

	// join secondary control plane nodes if any
	secondaryControlPlanes, err := nodes.SecondaryControlPlaneNodes(allNodes)
	if err != nil {
		return err
	}
	if len(secondaryControlPlanes) > 0 {
		if err := joinSecondaryControlPlanes(
			ctx, allNodes, secondaryControlPlanes,
		); err != nil {
			return err
		}
	}

	// then join worker nodes if any
	workers, err := nodes.SelectNodesByRole(allNodes, constants.WorkerNodeRoleValue)
	if err != nil {
		return err
	}
	if len(workers) > 0 {
		if err := joinWorkers(ctx, allNodes, workers); err != nil {
			return err
		}
	}

	return nil
}

func joinSecondaryControlPlanes(
	ctx *actions.ActionContext,
	allNodes []nodes.Node,
	secondaryControlPlanes []nodes.Node,
) error {
	ctx.Status.Start("Joining more control-plane nodes 🎮")
	defer ctx.Status.End(false)

	// TODO(bentheelder): it's too bad we can't do this concurrently
	// (this is not safe currently)
	for _, node := range secondaryControlPlanes {
		if err := runKubeadmJoinControlPlane(ctx, allNodes, &node); err != nil {
			return err
		}
	}

	ctx.Status.End(true)
	return nil
}

func joinWorkers(
	ctx *actions.ActionContext,
	allNodes []nodes.Node,
	workers []nodes.Node,
) error {
	ctx.Status.Start("Joining worker nodes 🚜")
	defer ctx.Status.End(false)

	// create a channel for receieving worker results
	errChan := make(chan error, len(workers))
	// create the workers concurrently
	var wg sync.WaitGroup
	for _, node := range workers {
		node := node // capture loop variable
		wg.Add(1)
		go func() {
			defer wg.Done()
			errChan <- runKubeadmJoin(ctx, allNodes, &node)
		}()
	}

	// wait for all workers to be done before closing the channel
	go func() {
		defer close(errChan)
		wg.Wait()
	}()

	// return the first error encountered if any
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	ctx.Status.End(true)
	return nil
}

// runKubeadmJoinControlPlane executes kubadm join --control-plane command
func runKubeadmJoinControlPlane(
	ctx *actions.ActionContext,
	allNodes []nodes.Node,
	node *nodes.Node,
) error {
	// get the join address
	joinAddress, err := getJoinAddress(ctx, allNodes)
	if err != nil {
		// TODO(bentheelder): logging here
		return err
	}

	// creates the folder tree for pre-loading necessary cluster certificates
	// on the joining node
	if err := node.Command("mkdir", "-p", "/etc/kubernetes/pki/etcd").Run(); err != nil {
		return errors.Wrap(err, "failed to join node with kubeadm")
	}

	// define the list of necessary cluster certificates
	fileNames := []string{
		"ca.crt", "ca.key",
		"front-proxy-ca.crt", "front-proxy-ca.key",
		"sa.pub", "sa.key",
		// TODO(someone): if we gain external etcd support these will be
		// handled differently
		"etcd/ca.crt", "etcd/ca.key",
	}

	// creates a temporary folder on the host that should acts as a transit area
	// for moving necessary cluster certificates
	tmpDir, err := fs.TempDir("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	err = os.MkdirAll(filepath.Join(tmpDir, "/etcd"), os.ModePerm)
	if err != nil {
		return err
	}

	// get the handle for the bootstrap control plane node (the source for necessary cluster certificates)
	controlPlaneHandle, err := nodes.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return err
	}

	// copies certificates from the bootstrap control plane node to the joining node
	for _, fileName := range fileNames {
		// sets the path of the certificate into a node
		containerPath := path.Join("/etc/kubernetes/pki", fileName)
		// set the path of the certificate into the tmp area on the host
		tmpPath := filepath.Join(tmpDir, fileName)
		// copies from bootstrap control plane node to tmp area
		if err := controlPlaneHandle.CopyFrom(containerPath, tmpPath); err != nil {
			return errors.Wrapf(err, "failed to copy certificate %s", fileName)
		}
		// copies from tmp area to joining node
		if err := node.CopyTo(tmpPath, containerPath); err != nil {
			return errors.Wrapf(err, "failed to copy certificate %s", fileName)
		}
	}

	// run kubeadm join --control-plane
	cmd := node.Command(
		"kubeadm", "join",
		// the join command uses the docker ip and a well know port that
		// are accessible only inside the docker network
		joinAddress,
		// set the node to join as control-plane
		"--experimental-control-plane",
		// uses a well known token and skips ca certification for automating TLS bootstrap process
		"--token", kubeadm.Token,
		"--discovery-token-unsafe-skip-ca-verification",
		// preflight errors are expected, in particular for swap being enabled
		// TODO(bentheelder): limit the set of acceptable errors
		"--ignore-preflight-errors=all",
		// increase verbosity for debug
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	log.Debug(strings.Join(lines, "\n"))
	if err != nil {
		return errors.Wrap(err, "failed to join a control plane node with kubeadm")
	}

	return nil
}

// runKubeadmJoin executes kubadm join command
func runKubeadmJoin(
	ctx *actions.ActionContext,
	allNodes []nodes.Node,
	node *nodes.Node,
) error {
	// get the join address
	joinAddress, err := getJoinAddress(ctx, allNodes)
	if err != nil {
		// TODO(bentheelder): logging here
		return err
	}

	// run kubeadm join
	cmd := node.Command(
		"kubeadm", "join",
		// the join command uses the docker ip and a well know port that
		// are accessible only inside the docker network
		joinAddress,
		// uses a well known token and skipping ca certification for automating TLS bootstrap process
		"--token", kubeadm.Token,
		"--discovery-token-unsafe-skip-ca-verification",
		// preflight errors are expected, in particular for swap being enabled
		// TODO(bentheelder): limit the set of acceptable errors
		"--ignore-preflight-errors=all",
		// increase verbosity for debugging
		"--v=6",
	)
	lines, err := exec.CombinedOutputLines(cmd)
	log.Debug(strings.Join(lines, "\n"))
	if err != nil {
		return errors.Wrap(err, "failed to join node with kubeadm")
	}

	return nil
}

// getJoinAddress return the join address thas is the control plane endpoint in case the cluster has
// an external load balancer in front of the control-plane nodes, otherwise the address of the
// boostrap control plane node.
func getJoinAddress(ctx *actions.ActionContext, allNodes []nodes.Node) (string, error) {
	// get the control plane endpoint, in case the cluster has an external load balancer in
	// front of the control-plane nodes
	controlPlaneEndpoint, err := nodes.GetControlPlaneEndpoint(allNodes)
	if err != nil {
		// TODO(bentheelder): logging here
		return "", err
	}

	// if the control plane endpoint is defined we are using it as a join address
	if controlPlaneEndpoint != "" {
		return controlPlaneEndpoint, nil
	}

	// otherwise, get the BootStrapControlPlane node
	controlPlaneHandle, err := nodes.BootstrapControlPlaneNode(allNodes)
	if err != nil {
		return "", err
	}

	// get the IP of the bootstrap control plane node
	controlPlaneIP, err := controlPlaneHandle.IP()
	if err != nil {
		return "", errors.Wrap(err, "failed to get IP for node")
	}

	return fmt.Sprintf("%s:%d", controlPlaneIP, kubeadm.APIServerPort), nil
}
