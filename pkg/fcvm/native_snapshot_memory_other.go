//go:build !linux

package fcvm

import (
	"context"
	"errors"
)

type unsupportedNativeSnapshotMemory struct{}

func newNativeSnapshotMemoryBackend() nativeSnapshotMemoryBackend {
	return unsupportedNativeSnapshotMemory{}
}
func (unsupportedNativeSnapshotMemory) CheckSupport() error {
	return errors.New("native snapshot memory: pinned cgroup v2 limits require Linux")
}
func (b unsupportedNativeSnapshotMemory) Open(context.Context, nativeLaunchRecord) (nativeSnapshotMemoryFence, error) {
	return nil, b.CheckSupport()
}
