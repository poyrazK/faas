//go:build !linux

package main

import "errors"

func acquireTestCampaignRecoveryGuard(legacyPath string) (func(), error) {
	return nil, errors.New("automatic recovery guards currently require Linux")
}
