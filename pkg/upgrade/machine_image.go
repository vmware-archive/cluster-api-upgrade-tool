// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
)

// updateMachineSpecImage replaces the value in spec specified by field with id.
func updateMachineSpecImage(spec *clusterv1.MachineSpec, field, id string) error {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec)
	if err != nil {
		return errors.Wrap(err, "error converting machine spec to unstructured")
	}

	pathParts := strings.Split(field, ".")
	if err := unstructured.SetNestedField(u, id, pathParts...); err != nil {
		return errors.Wrapf(err, "error setting machine spec field %q to %q", field, id)
	}

	s := clusterv1.MachineSpec{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u, &s); err != nil {
		return errors.Wrap(err, "error converting unstructured to machine spec")
	}

	*spec = s

	return nil
}
