//go:build !linux

package fcvm

func newNativeImageSourceBackend(string) nativeImageSourceBackend { return nil }
