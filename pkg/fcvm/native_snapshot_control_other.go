//go:build !linux

package fcvm

func newNativeSnapshotControlBackend() nativeSnapshotControlBackend { return nil }
