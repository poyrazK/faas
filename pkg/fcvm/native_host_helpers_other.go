//go:build !linux

package fcvm

import "errors"

func newNativeHostHelperGroups() nativeHostHelperGroups { return nil }

func nativeHostHelperStartTime(_ int) (uint64, error) {
	return 0, errors.New("native host helper recovery requires Linux cgroup v2")
}
