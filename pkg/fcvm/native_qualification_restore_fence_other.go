//go:build !linux

package fcvm

func newNativeQualificationRestoreFenceBackend() nativeQualificationRestoreFenceBackend { return nil }
