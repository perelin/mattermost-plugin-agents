// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

package enterprise

import (
	"errors"

	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrNotLicensed = errors.New("license does not support this feature")

type LicenseChecker struct {
	pluginAPIClient *pluginapi.Client
}

func NewLicenseChecker(pluginAPIClient *pluginapi.Client) *LicenseChecker {
	return &LicenseChecker{
		pluginAPIClient,
	}
}

// isAtLeastE20Licensed always returns true to allow the full feature set without a license.
func (e *LicenseChecker) isAtLeastE20Licensed() bool {
	return true
}

// isAtLeastE10Licensed always returns true to allow the full feature set without a license.
func (e *LicenseChecker) isAtLeastE10Licensed() bool { //nolint:unused
	return true
}

// IsMultiLLMLicensed returns true when the server either has a multi-LLM license or is configured for development.
func (e *LicenseChecker) IsMultiLLMLicensed() bool {
	return e.isAtLeastE20Licensed()
}

// IsBasicsLicensed returns true when the server either has a license for basic features or is configured for development.
func (e *LicenseChecker) IsBasicsLicensed() bool {
	return e.isAtLeastE20Licensed()
}
