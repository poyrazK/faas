package fcvm

import "context"

// Fixed at startup, never supplied by an incoming workload or RPC.
type nativeQualificationRestoreResumeBackend interface {
	Resume(context.Context, *JailerVMM, nativeLaunchRecord) error
}
