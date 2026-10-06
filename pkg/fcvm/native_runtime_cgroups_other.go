//go:build !linux

package fcvm

import (
	"context"
	"errors"
)

type unsupportedNativeRuntimeCgroups struct{}

func newNativeRuntimeCgroups() nativeRuntimeCgroups { return unsupportedNativeRuntimeCgroups{} }

func (unsupportedNativeRuntimeCgroups) Inventory(context.Context) ([]NativeRuntimeCgroup, error) {
	return nil, errors.New("native inventory: independent cgroup observations require Linux cgroup v2")
}
