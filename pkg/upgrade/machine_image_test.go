// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

func TestUpdateMachineSpecImage(t *testing.T) {
	machine := &clusterapiv1alpha1.Machine{
		Spec: clusterapiv1alpha1.MachineSpec{
			ProviderSpec: clusterapiv1alpha1.ProviderSpec{
				Value: &runtime.RawExtension{
					Raw: []byte(`{"foo":{"bar": "baz"}}`),
				},
			},
		},
	}

	err := updateMachineSpecImage(&machine.Spec, "providerSpec.value.foo.bar", "abcd1234")
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"foo":{"bar":"abcd1234"}}`), machine.Spec.ProviderSpec.Value.Raw)
}
