//go:build !linux

package fcvm

func newNativeTunBindBackend(string) nativeTunBindBackend { return nil }
