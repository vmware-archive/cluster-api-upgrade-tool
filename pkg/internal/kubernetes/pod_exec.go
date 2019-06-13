// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"bytes"
	"context"

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
}

func PodExec(ctx context.Context, input PodExecInput) (string, string, error) {
	req := input.KubernetesClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(input.Namespace).
		Name(input.Name).
		SubResource("exec")
	req.VersionedParams(&v1.PodExecOptions{
		Container: input.Container,
		Command:   input.Command,
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

	select {
	case err = <-errCh:
	case <-ctx.Done():
		return "", "", errors.New("pod exec timed out")
	}

	return stdout.String(), stderr.String(), err
}
