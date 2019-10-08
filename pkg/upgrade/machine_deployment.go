// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgrade

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type MachineDeploymentUpgrader struct {
	*base
	name     string
	selector labels.Selector
}

func NewMachineDeploymentUpgrader(log logr.Logger, config Config) (*MachineDeploymentUpgrader, error) {
	b, err := newBase(log, config)
	if err != nil {
		return nil, errors.Wrap(err, "error initializing upgrader")
	}

	var selector labels.Selector
	if config.MachineDeployment.LabelSelector != "" {
		// Step 1: parse the user-specified label selector
		selector, err = labels.Parse(config.MachineDeployment.LabelSelector)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing machine deployment label selector %q", config.MachineDeployment.LabelSelector)
		}

		// Step 2: add the cluster name to the label selector to avoid the possibility of selecting machine deployments
		// that belong to clusters other than the one the user specified.
		r, err := labels.NewRequirement(clusterv1.MachineClusterLabelName, selection.Equals, []string{b.clusterName})
		if err != nil {
			return nil, errors.Wrap(err, "error adding cluster name to label selector")
		}

		selector = selector.Add(*r)
	}

	return &MachineDeploymentUpgrader{
		base:     b,
		name:     config.MachineDeployment.Name,
		selector: selector,
	}, nil
}

func (u *MachineDeploymentUpgrader) Upgrade() error {
	var (
		machineDeployments *clusterv1.MachineDeploymentList
		err                error
	)

	if u.name != "" {
		key := ctrlclient.ObjectKey{
			Namespace: u.clusterNamespace,
			Name:      u.name,
		}

		var machineDeployment clusterv1.MachineDeployment

		if err := u.managerClusterClient.Get(context.TODO(), key, &machineDeployment); err != nil {
			return errors.Wrapf(err, "error getting machine deployment %q", u.name)
		}

		if machineDeployment.Labels == nil {
			return errors.Errorf("machine deployment is missing the %q label", clusterv1.MachineClusterLabelName)
		}
		if machineDeploymentCluster := machineDeployment.Labels[clusterv1.MachineClusterLabelName]; machineDeploymentCluster != u.clusterName {
			return errors.Errorf("machine deployment belongs to a different cluster (%q)", machineDeploymentCluster)
		}

		machineDeployments = &clusterv1.MachineDeploymentList{Items: []clusterv1.MachineDeployment{machineDeployment}}
	} else {
		machineDeployments, err = u.listMachineDeployments()
		if err != nil {
			return err
		}
	}

	if machineDeployments == nil || len(machineDeployments.Items) == 0 {
		return errors.New("Found 0 machine deployments")
	}

	return u.upgradeMachineDeployments(machineDeployments)
}

func (u *MachineDeploymentUpgrader) listMachineDeployments() (*clusterv1.MachineDeploymentList, error) {
	listOptions := []ctrlclient.ListOption{
		ctrlclient.InNamespace(u.clusterNamespace),
	}

	var selectorText string

	if u.selector == nil {
		matchingLabels := ctrlclient.MatchingLabels{clusterv1.MachineClusterLabelName: u.clusterName}
		selectorText = labels.SelectorFromSet(labels.Set(matchingLabels)).String()
		listOptions = append(listOptions, matchingLabels)
	} else {
		selectorText = u.selector.String()
		listOptions = append(listOptions, ctrlclient.MatchingLabelsSelector{Selector: u.selector})
	}

	u.log.Info("Listing machine deployments", "label-selector", selectorText)

	list := &clusterv1.MachineDeploymentList{}
	err := u.managerClusterClient.List(context.TODO(), list, listOptions...)
	if err != nil {
		return nil, errors.Wrap(err, "error listing machines")
	}

	return list, nil
}

func (u *MachineDeploymentUpgrader) upgradeMachineDeployments(list *clusterv1.MachineDeploymentList) error {
	for _, machineDeployment := range list.Items {
		// Skip any machineDeployments that already have this upgrade annotation id
		if val, ok := machineDeployment.Spec.Template.Annotations[UpgradeIDAnnotationKey]; ok && val == u.upgradeID {
			continue
		}
		if err := u.updateMachineDeployment(&machineDeployment); err != nil {
			u.log.Error(err, "Failed to create new MachineDeployment", "namespace", machineDeployment.Namespace, "name", machineDeployment.Name)
			return err
		}
	}
	return nil
}

func (u *MachineDeploymentUpgrader) updateMachineDeployment(machineDeployment *clusterv1.MachineDeployment) error {
	u.log.Info("Updating MachineDeployment", "namespace", machineDeployment.Namespace, "name", machineDeployment.Name)

	// Get the original, pre-modified version in json
	patch := ctrlclient.MergeFrom(machineDeployment.DeepCopy())

	// Make the modification(s)
	desiredVersion := u.desiredVersion.String()
	machineDeployment.Spec.Template.Spec.Version = &desiredVersion

	// Add the upgrade ID to this template so all machines get it
	if machineDeployment.Spec.Template.Annotations == nil {
		machineDeployment.Spec.Template.Annotations = map[string]string{}
	}
	machineDeployment.Spec.Template.Annotations[UpgradeIDAnnotationKey] = u.upgradeID

	if u.imageField != "" && u.imageID != "" {
		if err := updateMachineSpecImage(&machineDeployment.Spec.Template.Spec, u.imageField, u.imageID); err != nil {
			return err
		}
	}

	// Get the updated version in json
	if err := u.managerClusterClient.Patch(context.TODO(), machineDeployment, patch); err != nil {
		return errors.Wrapf(err, "error patching machinedeployment %s", machineDeployment.Name)
	}

	return nil
}
