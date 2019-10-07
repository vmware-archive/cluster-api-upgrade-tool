// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
)

func TestUpdateMachineSpecImage(t *testing.T) {
	machine := &clusterv1.Machine{
		Spec: clusterv1.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				Name: "foo",
			},
		},
	}

	err := updateMachineSpecImage(&machine.Spec, "infrastructureRef.name", "bar")
	require.NoError(t, err)
	assert.Equal(t, "bar", machine.Spec.InfrastructureRef.Name)
}
