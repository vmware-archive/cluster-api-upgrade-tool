// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"strings"

	"github.com/pkg/errors"
)

// ParseProviderID is a convenience function to pull out the provider ID
// @TODO add switch case based on different providers
func ParseProviderID(providerID string) (string, error) {
	parts := strings.Split(providerID, "/")
	if len(parts) == 0 {
		return "", errors.Errorf("Unable to parse providerID: %s", providerID)
	}

	id := parts[len(parts)-1]
	return id, nil
}
