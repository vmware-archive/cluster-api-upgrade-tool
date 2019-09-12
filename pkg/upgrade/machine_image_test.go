// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	//"k8s.io/apimachinery/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	clusterapiv1alpha2 "sigs.k8s.io/cluster-api/api/v1alpha2"
)

func TestUpdateMachineSpecImage(t *testing.T) {
	machine := &clusterapiv1alpha2.Machine{
		Spec: clusterapiv1alpha2.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				Name: "foo",
			},
		},
	}

	err := updateMachineSpecImage(&machine.Spec, "infrastructureRef.name", "bar")
	require.NoError(t, err)
	assert.Equal(t, "bar", machine.Spec.InfrastructureRef.Name)
}
