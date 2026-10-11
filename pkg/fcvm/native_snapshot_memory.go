package fcvm

// adr: 796 — temporary capture limits stay bound to the original native cgroup.
import "context"

type nativeSnapshotMemoryFence interface {
	Widen(context.Context) error
	Restore(context.Context) error
	Close() error
}

type nativeSnapshotMemoryBackend interface {
	CheckSupport() error
	Open(context.Context, nativeLaunchRecord) (nativeSnapshotMemoryFence, error)
}
