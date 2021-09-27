// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

func TestEtcdMemberHealthStructDecoding(t *testing.T) {
	data := `{
		"header": {
			"cluster_id":14841639068965178418,
			"member_id":10276657743932975437,
			"raft_term":444
		},
		"members": [
			{
				"ID":5782640540428238474,
				"name":"two",
				"peerURLs":["http://localhost:3380"],
				"clientURLs":["http://localhost:3379"]
			},
			{
				"ID":10276657743932975437,
				"name":"default",
				"peerURLs":["http://localhost:2380"],
				"clientURLs":["http://localhost:2379"]
			}
		]
	}`

	var r etcdMembersResponse

	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("%+v", err)
	}

	expected := etcdMembersResponse{
		Members: []etcdMember{
			{Name: "two", ID: 5782640540428238474, ClientURLs: []string{"http://localhost:3379"}},
			{Name: "default", ID: 10276657743932975437, ClientURLs: []string{"http://localhost:2379"}},
		},
	}

	if !reflect.DeepEqual(expected, r) {
		t.Errorf("expected %#v, got %#v", expected, r)
	}
}

func TestUpdateKubeadmKubernetesVersion(t *testing.T) {
	generate := func(version string) string {
		return fmt.Sprintf(`apiVersion: v1
data:
  ClusterConfiguration: |
    apiServer:
      certSANs:
      - 10.0.0.227
      - example.com
      extraArgs:
        authorization-mode: Node,RBAC
        cloud-provider: aws
      timeoutForControlPlane: 4m0s
    apiVersion: kubeadm.k8s.io/v1beta1
    certificatesDir: /etc/kubernetes/pki
    clusterName: test1
    controlPlaneEndpoint: example.com:6443
    controllerManager:
      extraArgs:
        cloud-provider: aws
    dns:
      type: CoreDNS
    etcd:
      local:
        dataDir: /var/lib/etcd
    imageRepository: k8s.gcr.io
    kind: ClusterConfiguration
    kubernetesVersion: %s
    networking:
      dnsDomain: cluster.local
      podSubnet: 192.168.0.0/16
      serviceSubnet: 10.96.0.0/12
    scheduler: {}
  ClusterStatus: |
    apiEndpoints:
      ip-10-0-0-197.ec2.internal:
        advertiseAddress: 10.0.0.197
        bindPort: 6443
      ip-10-0-0-227.ec2.internal:
        advertiseAddress: 10.0.0.227
        bindPort: 6443
    apiVersion: kubeadm.k8s.io/v1beta1
    kind: ClusterStatus
kind: ConfigMap
metadata:
  creationTimestamp: "2019-07-03T18:17:01Z"
  name: kubeadm-config
  namespace: kube-system
  resourceVersion: "1312"
  selfLink: /api/v1/namespaces/kube-system/configmaps/kubeadm-config
  uid: c0d8ace7-9dbe-11e9-bfe7-129245863a50
`, version)
	}

	originalYaml := generate("v1.13.7")

	updatedVersion := "v1.14.3"
	expectedYaml := generate(updatedVersion)

	original := new(v1.ConfigMap)
	_, _, err := scheme.Codecs.UniversalDecoder(v1.SchemeGroupVersion).Decode([]byte(originalYaml), nil, original)
	if err != nil {
		t.Fatal(err)
	}

	updatedCM, err := updateKubeadmKubernetesVersion(original, updatedVersion)
	if err != nil {
		t.Fatal(err)
	}

	updatedYaml, err := yaml.Marshal(updatedCM)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(expectedYaml) != strings.TrimSpace(string(updatedYaml)) {
		t.Errorf("expected %s, got %s", expectedYaml, updatedYaml)
	}
}

func TestMachineOwnerFiltration(t *testing.T) {
	machineRef := metav1.OwnerReference{
		APIVersion: clusterv1.GroupVersion.String(),
		Kind:       "Machine",
		Name:       "Someone",
		UID:        "abcdefg",
	}

	fooRef := metav1.OwnerReference{
		APIVersion: "foo.bar.xyz/v1alpha1",
		Kind:       "Baz",
		Name:       "Something",
		UID:        "abc123",
	}

	tests := []struct {
		name           string
		ownerRefs      []metav1.OwnerReference
		expectedLength int
	}{
		{
			name:           "with machine ref",
			ownerRefs:      []metav1.OwnerReference{machineRef},
			expectedLength: 0,
		},
		{
			name:           "without machine ref",
			ownerRefs:      []metav1.OwnerReference{fooRef},
			expectedLength: 1,
		},
		{
			name:           "with machine ref and others",
			ownerRefs:      []metav1.OwnerReference{machineRef, fooRef},
			expectedLength: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filtered := filterOutMachineOwners(tc.ownerRefs)
			assert.Len(t, filtered, tc.expectedLength)
		})
	}
}

func TestGenerateMachineName(t *testing.T) {
	maxNameLength := validation.DNS1123SubdomainMaxLength
	upgradeID := "a1b2c3d4e5f.6g7h-8i9j0k"
	suffix := "-" + upgradeID
	maxNameLengthWithoutTrimming := maxNameLength - len(suffix)

	tests := []struct {
		name     string
		base     string
		expected string
	}{
		{
			name:     "control plane",
			base:     "controlplane-0",
			expected: "controlplane-0" + suffix,
		},
		{
			name:     "max length without trimming",
			base:     strings.Repeat("s", maxNameLengthWithoutTrimming),
			expected: strings.Repeat("s", maxNameLengthWithoutTrimming) + suffix,
		},
		{
			name:     "trimming",
			base:     strings.Repeat("s", maxNameLength),
			expected: strings.Repeat("s", maxNameLengthWithoutTrimming) + suffix,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := generateReplacementMachineName(tc.base, upgradeID)
			if tc.expected != actual {
				t.Errorf("expected %q (len %d), got %q (len %d)", tc.expected, len(tc.expected), actual, len(actual))
			}
		})
	}
}

func TestShouldSkipMachine(t *testing.T) {
	valid := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationUpgradeID:       "abc",
				AnnotationMachineNameBase: "base",
			},
			Namespace: "default",
			Name:      "base",
		},
		Spec: clusterv1.MachineSpec{
			ProviderID: pointer.StringPtr("provider-id"),
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: &corev1.ObjectReference{
					Kind:       "KubeadmConfig",
					Namespace:  "default",
					Name:       "base-hc",
					UID:        "some-uid",
					APIVersion: "bootstrap.cluster.x-k8s.io/v1alpha2",
				},
			},
		},
	}

	missingAnnotations := valid.DeepCopy()
	missingAnnotations.Annotations = make(map[string]string)

	missingProviderID := valid.DeepCopy()
	missingProviderID.Spec.ProviderID = nil

	differentUpgradeID := valid.DeepCopy()
	differentUpgradeID.Annotations[AnnotationUpgradeID] = "something-else"

	missingMachineNameBase := valid.DeepCopy()
	delete(missingMachineNameBase.Annotations, AnnotationMachineNameBase)

	replacementMachine := valid.DeepCopy()
	replacementMachine.Name = "base-abc"

	missingBootstrapConfigRef := valid.DeepCopy()
	missingBootstrapConfigRef.Spec.Bootstrap.ConfigRef = nil

	otherBootstrapConfigRefAPIGroup := valid.DeepCopy()
	otherBootstrapConfigRefAPIGroup.Spec.Bootstrap.ConfigRef.APIVersion = "some-other-group/v1alpha2"

	otherBootstrapConfigRefKind := valid.DeepCopy()
	otherBootstrapConfigRefKind.Spec.Bootstrap.ConfigRef.Kind = "SomeOtherKind"

	tests := []struct {
		name       string
		machine    *clusterv1.Machine
		expectSkip bool
	}{
		{
			name:       "missing annotations",
			machine:    missingAnnotations,
			expectSkip: true,
		},
		{
			name:       "missing provider id",
			machine:    missingProviderID,
			expectSkip: true,
		},
		{
			name:       "different upgrade id",
			machine:    differentUpgradeID,
			expectSkip: true,
		},
		{
			name:       "missing machine name base",
			machine:    missingMachineNameBase,
			expectSkip: true,
		},
		{
			name:       "replacement machine",
			machine:    replacementMachine,
			expectSkip: true,
		},
		{
			name:       "missing bootstrap config ref",
			machine:    missingProviderID,
			expectSkip: true,
		},
		{
			name:       "other bootstrap config ref api group",
			machine:    otherBootstrapConfigRefAPIGroup,
			expectSkip: true,
		},
		{
			name:       "other bootstrap config ref kind",
			machine:    otherBootstrapConfigRefKind,
			expectSkip: true,
		},
		{
			name:       "valid",
			machine:    valid,
			expectSkip: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := &ControlPlaneUpgrader{
				upgradeID: "abc",
				log:       log.NullLogger{},
			}

			if e, a := tc.expectSkip, u.shouldSkipMachine(log.NullLogger{}, tc.machine); e != a {
				t.Errorf("expected %t, got %t", e, a)
			}
		})
	}
}

func TestPatchMachine(t *testing.T) {
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cp-0-foobar",
			Labels: map[string]string{
				"hello": "world",
			},
			Annotations: map[string]string{
				AnnotationMachineNameBase: "cp-0",
			},
		},
	}

	patch, err := jsonpatch.DecodePatch([]byte(`[{ "op": "remove", "path": "/metadata/labels/hello" }]`))
	if err != nil {
		t.Fatal(err)
	}

	patched, err := patchMachine(machine, patch)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{}, patched.Labels)
}

func TestPatchRuntimeObject(t *testing.T) {
	u := new(unstructured.Unstructured)
	u.Object = map[string]interface{}{
		"apiVersion": "cluster.x-k8s.io/v1alpha3",
		"kind":       "Cluster",
		"a":          "b",
	}

	patch, err := jsonpatch.DecodePatch([]byte(`[{ "op": "add", "path": "/metadata", "value": {"labels":{ "hello":"world"}} }]`))
	if err != nil {
		t.Fatal(err)
	}

	patched, err := patchRuntimeObject(u, patch)
	if err != nil {
		t.Fatal(err)
	}

	assert.IsType(t, new(unstructured.Unstructured), patched)
	patchedU := patched.(*unstructured.Unstructured)
	labels, found, err := unstructured.NestedMap(patchedU.Object, "metadata", "labels")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]interface{}{"hello": "world"}, labels)

	namespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "name",
		},
	}

	patched, err = patchRuntimeObject(namespace, patch)
	if err != nil {
		t.Fatal(err)
	}

	assert.IsType(t, new(v1.Namespace), patched)
	patchedNamespace := patched.(*v1.Namespace)
	assert.Equal(t, map[string]string{"hello": "world"}, patchedNamespace.Labels)

}

func TestReconcileKubeadmConfigMapClusterStatusAPIEndpoints(t *testing.T) {
	match1 := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "match-1",
		},
	}

	match2 := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "match-2",
		},
	}

	originalClusterStatusYAML := `apiEndpoints:
  match-1:
    advertiseAddress: 10.0.0.197
    bindPort: 6443
  not-a-match:
    advertiseAddress: 10.0.0.111
    bindPort: 6443
  match-2:
    advertiseAddress: 10.0.0.227
    bindPort: 6443
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterStatus
`

	originalCM := &v1.ConfigMap{
		Data: map[string]string{
			"ClusterStatus": originalClusterStatusYAML,
		},
	}

	nodeList := &v1.NodeList{
		Items: []v1.Node{match1, match2},
	}

	updatedCM, err := reconcileKubeadmConfigMapClusterStatusAPIEndpoints(originalCM, nodeList)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedCM := &v1.ConfigMap{
		Data: map[string]string{
			"ClusterStatus": `apiEndpoints:
  match-1:
    advertiseAddress: 10.0.0.197
    bindPort: 6443
  match-2:
    advertiseAddress: 10.0.0.227
    bindPort: 6443
apiVersion: kubeadm.k8s.io/v1beta1
kind: ClusterStatus
`,
		},
	}

	if e, a := expectedCM.Data["ClusterStatus"], updatedCM.Data["ClusterStatus"]; e != a {
		t.Errorf("expected %s, got %s", e, a)
	}
}

func TestHasNodeLabel(t *testing.T) {
	nodeWithLabel := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
			Labels: map[string]string{
				LabelNodeRoleMaster: "",
			},
		},
	}
	nodeWithoutLabel := nodeWithLabel.DeepCopy()
	nodeWithoutLabel.SetLabels(make(map[string]string))

	tests := []struct {
		name     string
		node     *corev1.Node
		expected bool
	}{
		{
			name:     "node has label",
			node:     nodeWithLabel,
			expected: true,
		},
		{
			name:     "node is not found",
			node:     nil,
			expected: false,
		},
		{
			name:     "node does not have label",
			node:     nodeWithoutLabel,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient *fake.Clientset

			if tt.node != nil {
				fakeClient = fake.NewSimpleClientset(tt.node)
			} else {
				fakeClient = fake.NewSimpleClientset()
			}
			u := &ControlPlaneUpgrader{
				log:                    log.NullLogger{},
				targetKubernetesClient: fakeClient,
				upgradeID:              "abc",
			}

			actual := u.hasMasterNodeLabel("foo")
			if tt.expected != actual {
				t.Errorf("expected %t, got %t", tt.expected, actual)
			}
		})
	}
}

func TestIsReplacementMachine(t *testing.T) {
	upgradeID := "foobar"
	tests := []struct {
		name     string
		machine  *clusterv1.Machine
		expected bool
	}{
		{
			name: "machine is a replacement",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cp-0-foobar",
					Annotations: map[string]string{
						AnnotationUpgradeID:       upgradeID,
						AnnotationMachineNameBase: "cp-0",
					},
				},
			},
			expected: true,
		},
		{
			name: "machine is an original control plane machine",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cp-0",
				},
			},
			expected: false,
		},
		{
			name: "machine belongs to a different upgrade",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cp-0-another-upgrade",
					Annotations: map[string]string{
						AnnotationUpgradeID:       upgradeID,
						AnnotationMachineNameBase: "cp-0",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &ControlPlaneUpgrader{
				log:       log.NullLogger{},
				upgradeID: upgradeID,
			}

			actual := u.isReplacementMachine(tt.machine)
			if actual != tt.expected {
				t.Errorf("expected %t, but got %t", tt.expected, actual)
			}
		})
	}
}
