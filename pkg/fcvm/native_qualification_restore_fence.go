package fcvm

import "context"

// Read-only original cgroup custody. This grants no snapshot headroom write.
type nativeQualificationRestoreFence interface {
	Group() nativeHostHelperGroup
	Require(context.Context) error
	Close() error
}
type nativeQualificationRestoreFenceBackend interface {
	Pin(context.Context, nativeLaunchRecord) (nativeQualificationRestoreFence, error)
}
