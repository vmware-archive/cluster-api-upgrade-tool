// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0
package kubernetes

import (
	"bytes"
	"time"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type PodExecInput struct {
	RestConfig       *rest.Config
	KubernetesClient kubernetes.Interface
	Namespace        string
	Name             string
	Container        string
	Command          []string
	Timeout          time.Duration
}

func PodExec(input PodExecInput) (string, string, error) {
	req := input.KubernetesClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(input.Namespace). //"kube-system").
		Name(input.Name).           //"etcd-ip-10-0-0-61.us-west-2.compute.internal").
		SubResource("exec")
	req.VersionedParams(&v1.PodExecOptions{
		Container: input.Container,
		Command:   input.Command, //[]string{"etcdctl", "--ca-file", "/etc/kubernetes/pki/etcd/ca.crt", "--cert-file", "/etc/kubernetes/pki/etcd/peer.crt", "--key-file", "/etc/kubernetes/pki/etcd/peer.key", "--endpoints=https://10.0.0.61:2379", "cluster-health"}, // @TODO: edit this to compose later
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(input.RestConfig, "POST", req.URL())
	if err != nil {
		return "", "", errors.Wrap(err, "error creating executor for pod exec")
	}

	var stdout, stderr bytes.Buffer
	streamOptions := remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}

	errCh := make(chan error)

	go func() {
		errCh <- executor.Stream(streamOptions)
	}()

	var timeoutCh <-chan time.Time
	if input.Timeout > 0 {
		timer := time.NewTimer(input.Timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case err = <-errCh:
	case <-timeoutCh:
		return "", "", errors.Errorf("pod exec timed out after %v", input.Timeout)
	}

	return stdout.String(), stderr.String(), err
}
