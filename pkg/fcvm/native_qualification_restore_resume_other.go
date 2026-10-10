//go:build !linux

package fcvm

func newNativeQualificationRestoreResumeBackend() nativeQualificationRestoreResumeBackend { return nil }
