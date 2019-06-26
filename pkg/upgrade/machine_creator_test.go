// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade_test

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/vmware/cluster-api-upgrade-tool/pkg/upgrade"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

type log struct{}

func (l *log) Error(err error, msg string, keysAndValues ...interface{}) {}
func (l *log) V(level int) logr.InfoLogger                               { return nil }
func (l *log) WithValues(keysAndValues ...interface{}) logr.Logger       { return l }
func (l *log) WithName(name string) logr.Logger                          { return l }
func (l *log) Info(msg string, keysAndValues ...interface{})             {}
func (l *log) Enabled() bool                                             { return false }

type client struct {
	machine *clusterapiv1alpha1.Machine
	nodes   *v1.NodeList
}

func (m *client) Create(machine *clusterapiv1alpha1.Machine) (*clusterapiv1alpha1.Machine, error) {
	return m.machine, nil
}
func (m *client) Get(string, metav1.GetOptions) (*clusterapiv1alpha1.Machine, error) {
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

func TestNewMachine(t *testing.T) {
	providerID := "localhost:////my-machine-identifier"
	mc := upgrade.NewMachineCreator(
		upgrade.WithLogger(&log{}),
		upgrade.WithMachineCreator(&client{
			machine: &clusterapiv1alpha1.Machine{},
		}),
		upgrade.WithMachineGetter(&client{
			machine: &clusterapiv1alpha1.Machine{
				Spec: clusterapiv1alpha1.MachineSpec{
					ProviderID: &providerID,
				},
			},
		}),
		upgrade.WithNodeLister(&client{
			nodes: &v1.NodeList{
				Items: []v1.Node{
					{Spec: v1.NodeSpec{ProviderID: "some-other-machine"}},
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
	machineToReplace := &clusterapiv1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testmachine-one",
		},
	}
	out, node, err := mc.NewMachine(machineToReplace)
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
