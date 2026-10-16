//go:build !linux

package main

import "os"

func inspectTestCampaignWatchGuard(path string) (string, int) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "absent", 0
	}
	return "unverifiable", 0
}
