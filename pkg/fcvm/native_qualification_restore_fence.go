package fcvm

import (
	"context"
	"errors"
)

// Read-only original cgroup custody. This grants no snapshot headroom write.
type nativeQualificationRestoreFence interface {
	Group() nativeHostHelperGroup
	Require(context.Context) error
	Close() error
}
type nativeQualificationRestoreFenceBackend interface {
	Pin(context.Context, nativeLaunchRecord) (nativeQualificationRestoreFence, error)
}

func requireNativeRestoreFenceGroup(original, current nativeHostHelperGroup) error {
	if original != current {
		return errors.New("native restore load: pinned original cgroup identity changed")
	}
	return nil
}
