// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterapiv1alpha2 "sigs.k8s.io/cluster-api/api/v1alpha2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type log struct{}

func (l *log) Error(err error, msg string, keysAndValues ...interface{}) {}
func (l *log) V(level int) logr.InfoLogger                               { return nil }
func (l *log) WithValues(keysAndValues ...interface{}) logr.Logger       { return l }
func (l *log) WithName(name string) logr.Logger                          { return l }
func (l *log) Info(msg string, keysAndValues ...interface{})             {}
func (l *log) Enabled() bool                                             { return false }

type client struct {
	machine *clusterapiv1alpha2.Machine
	nodes   *v1.NodeList
}

func (m *client) Create(machine *clusterapiv1alpha2.Machine) (*clusterapiv1alpha2.Machine, error) {
	return m.machine, nil
}
func (m *client) Get(string, metav1.GetOptions) (*clusterapiv1alpha2.Machine, error) {
	return m.machine, nil
}
func (m *client) List(options metav1.ListOptions) (*v1.NodeList, error) {
	return m.nodes, nil
}

type pclient struct {
	pod *v1.Pod
}

func (p *pclient) Get(string, metav1.GetOptions) (*v1.Pod, error) {
	return p.pod, nil
}

type GetMachineMock struct {
	ctrlclient ctrlclient.Client
	providerID string
}

func (m *GetMachineMock) Get(name string, namespace string) (*clusterapiv1alpha2.Machine, error) {
	machine := &clusterapiv1alpha2.Machine{
		Spec: clusterapiv1alpha2.MachineSpec{
			ProviderID: &m.providerID,
		},
	}
	if err := m.ctrlclient.Get(context.TODO(), ctrlclient.ObjectKey{Namespace: namespace, Name: name}, machine); err != nil {
		return nil, err
	}

	return machine, nil
}

func TestNewMachine(t *testing.T) {
	providerID := "localhost:////my-machine-identifier"

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(
		clusterapiv1alpha2.GroupVersion,
		&clusterapiv1alpha2.Machine{},
	)
	fake := fake.NewFakeClientWithScheme(scheme)

	mc := upgrade.NewMachineCreator(
		upgrade.WithMachineGetter(&GetMachineMock{ctrlclient: fake, providerID: providerID}),
		upgrade.WithControllerRuntimeClient(fake),
		upgrade.WithLogger(&log{}),
		upgrade.WithNamespace("test"),
		upgrade.WithNodeLister(&client{
			nodes: &v1.NodeList{
				Items: []v1.Node{
					{
						Spec: v1.NodeSpec{
							ProviderID: "localhost:///some-other-machine",
						},
					},
					{
						Spec: v1.NodeSpec{
							ProviderID: "localhost:///my-local-zone/my-machine-identifier",
						},
						Status: v1.NodeStatus{
							Addresses: []v1.NodeAddress{
								{
									Type:    v1.NodeHostName,
									Address: "my-address",
								},
							},
						},
					},
				},
			},
		}),
		upgrade.WithPodGetter(&pclient{
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: []v1.PodCondition{
						{
							Status: v1.ConditionTrue,
							Type:   v1.PodScheduled,
						},
						{
							Status: v1.ConditionTrue,
							Type:   v1.PodInitialized,
						},
						{
							Status: v1.ConditionTrue,
							Type:   v1.PodReady,
						},
						{
							Status: v1.ConditionTrue,
							Type:   v1.ContainersReady,
						},
					},
				},
			},
		}),
		upgrade.WithMatchingNodeTimeout(5*time.Second),
		upgrade.WithNodeReadyTimeout(10*time.Second),
		upgrade.WithProviderIDTimeout(5*time.Second),
	)
	machineToReplace := &clusterapiv1alpha2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testmachine-one-replace",
		},
	}
	out, node, err := mc.NewMachine("testmachine-one-two", machineToReplace)
	logrus.Infoln(out)
	logrus.Infoln(node)
	if err != nil {
		t.Fatalf("%+v", err)
	}
	if out == nil {
		t.Fatal("out machine is nil?")
	}
	if node == nil {
		t.Fatal("node is nil?")
	}
}
