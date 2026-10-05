//go:build !linux

package fcvm

func newNativeJailDeviceBackend(string) nativeJailDeviceBackend { return nil }
