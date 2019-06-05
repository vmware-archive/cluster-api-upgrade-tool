// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterapiv1alpha1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
)

type MachineDeploymentUpgrader struct {
	*base
}

func NewMachineDeploymentUpgrader(config Config) (*MachineDeploymentUpgrader, error) {
	b, err := newBase(config)
	if err != nil {
		logrus.WithError(err).Error("Error initializing upgrader")
		return nil, errors.Wrap(err, "error initializing upgrader")
	}

	return &MachineDeploymentUpgrader{
		base: b,
	}, nil
}

func (u *MachineDeploymentUpgrader) Upgrade() error {
	machineDeployments, err := u.listMachineDeployments()
	if err != nil {
		return err
	}

	if machineDeployments == nil || len(machineDeployments.Items) == 0 {
		return errors.New("Found 0 machine deployments")
	}

	return u.upgradeMachineDeployments(machineDeployments)
}

func (u *MachineDeploymentUpgrader) listMachineDeployments() (*clusterapiv1alpha1.MachineDeploymentList, error) {
	logrus.Info("Listing machine deployments")

	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("cluster.k8s.io/cluster-name=%s,set=node", u.clusterName),
	}

	machineDeployments, err := u.managementClusterAPIClient.MachineDeployments(u.clusterNamespace).List(listOptions)
	if err != nil {
		return nil, errors.Wrap(err, "error listing machines")
	}

	return machineDeployments, nil
}

func (u *MachineDeploymentUpgrader) upgradeMachineDeployments(list *clusterapiv1alpha1.MachineDeploymentList) error {
	for _, machineDeployment := range list.Items {
		if err := u.updateMachineDeployment(&machineDeployment); err != nil {
			logrus.Errorf("Failed to create new machineDeployment(Name:%s),Error: %s", machineDeployment.Name, err.Error())
			return err
		}
	}
	return nil
}

func (u *MachineDeploymentUpgrader) updateMachineDeployment(machineDeployment *clusterapiv1alpha1.MachineDeployment) error {
	logrus.Infof("Updating machine deployment : %#v", machineDeployment)

	// Get the original, pre-modified version in json
	original, err := json.Marshal(machineDeployment)
	if err != nil {
		return errors.Wrap(err, "error marshaling original machine deployment to json")
	}

	// Make the modification(s)
	machineDeployment.Spec.Template.Spec.Versions.Kubelet = u.desiredVersion.String()

	// Get the updated version in json
	updated, err := json.Marshal(machineDeployment)
	if err != nil {
		return errors.Wrap(err, "error marshaling updated machine deployment to json")
	}

	// Create the patch
	patchBytes, err := jsonpatch.CreateMergePatch(original, updated)
	if err != nil {
		return errors.Wrap(err, "error creating json patch")
	}

	_, err = u.managementClusterAPIClient.MachineDeployments(u.clusterNamespace).Patch(machineDeployment.Name, types.MergePatchType, patchBytes)
	if err != nil {
		return errors.Wrapf(err, "error patching machinedeployment %s", machineDeployment.Name)
	}

	return nil
}
