package runtimeconfig

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// ObjectStorageFenceInterval is how often a gateway re-reads the fence, as
// often as the watcher re-reads s3_enabled.
const ObjectStorageFenceInterval = 5 * time.Second

// TimelineStore reads the object storage timeline fence. It is not
// state.Store, so edge daemons keep their narrow store type.
type TimelineStore interface {
	ObjectStorageTimelines(ctx context.Context) (accepted, current string, err error)
}

// ObjectStorageFence holds object storage serving off while the database runs
// on a PostgreSQL timeline nobody has accepted (ADR-20261025111015
// amendment). A documented restore recovers from the WAL archive and
// promotes onto a new timeline; s3_enabled comes back as it was at the
// restore point, and with it every S3 key and grant revoked since, so the S3
// gateway and public reads must wait for `gregalectl object-storage accept`.
// The fence is closed until its first read; a failed read keeps the last
// answer. A database with no acceptance yet (before the first start of a
// release that knows the fence) is open.
type ObjectStorageFence struct {
	store TimelineStore
	log   *slog.Logger
	open  atomic.Bool
}

// NewObjectStorageFence returns a closed fence over store.
func NewObjectStorageFence(store TimelineStore, log *slog.Logger) *ObjectStorageFence {
	if log == nil {
		log = slog.Default()
	}
	return &ObjectStorageFence{store: store, log: log}
}

// Open reports whether object storage may serve.
func (f *ObjectStorageFence) Open() bool { return f != nil && f.open.Load() }

// Check reads the timelines once.
func (f *ObjectStorageFence) Check(ctx context.Context) error {
	accepted, current, err := f.store.ObjectStorageTimelines(ctx)
	if err != nil {
		return err
	}
	open := accepted == "" || accepted == current
	if was := f.open.Swap(open); was && !open {
		f.log.Warn("object storage fenced: the database runs on a timeline nobody accepted; reconcile, then run gregalectl object-storage accept",
			"accepted_timeline", accepted, "current_timeline", current)
	} else if !was && open {
		f.log.Info("object storage fence open", "timeline", current)
	}
	return nil
}

// Run re-reads the fence every ObjectStorageFenceInterval until ctx ends.
func (f *ObjectStorageFence) Run(ctx context.Context) {
	ticker := time.NewTicker(ObjectStorageFenceInterval)
	defer ticker.Stop()
	for {
		if err := f.Check(ctx); err != nil && ctx.Err() == nil {
			f.log.Warn("object storage fence: read timelines failed; keeping the last answer", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
