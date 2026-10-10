package snapshothipd

import (
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// memoryShareQueue bounds pending sharing passes. A dropped job is not lost:
// revalidation re-offers every cached snapshot every few minutes until its
// memory file carries the shared marker.
const memoryShareQueue = 256

// shareSnapshotMemory is the storage pass; tests replace it.
var shareSnapshotMemory = func(ctx context.Context, index *storage.MemoryShareIndex, backend storage.StorageBackend, memKey string, layerKeys []string) (storage.MemoryShareResult, error) {
	return index.ShareSnapshotMemory(ctx, backend, memKey, layerKeys)
}

// memorySharer runs ADR-911 page sharing off the replica path: one pass reads
// the app layer and the memory file, so it must not delay marking a replica
// ready or claiming the next job.
type memorySharer struct {
	index   *storage.MemoryShareIndex
	jobs    chan state.SnapshotReplicaJob
	mu      sync.Mutex
	pending map[string]bool
}

func newMemorySharer() *memorySharer {
	return &memorySharer{
		index:   storage.NewMemoryShareIndex(),
		jobs:    make(chan state.SnapshotReplicaJob, memoryShareQueue),
		pending: map[string]bool{},
	}
}

// WithMemorySharing turns ADR-911 snapshot memory page sharing on or off.
func (r *Runner) WithMemorySharing(enabled bool) *Runner {
	if enabled {
		r.memory = newMemorySharer()
	} else {
		r.memory = nil
	}
	return r
}

// offerMemoryShare queues job's memory for sharing without blocking.
func (r *Runner) offerMemoryShare(job state.SnapshotReplicaJob) {
	m := r.memory
	if m == nil || job.StorageKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending[job.StorageKey] {
		return
	}
	select {
	case m.jobs <- job:
		m.pending[job.StorageKey] = true
	default:
	}
}

func (r *Runner) runMemorySharing(ctx context.Context) {
	m := r.memory
	if m == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.jobs:
			r.shareMemory(ctx, m, job)
			m.mu.Lock()
			delete(m.pending, job.StorageKey)
			m.mu.Unlock()
		}
	}
}

func (r *Runner) shareMemory(ctx context.Context, m *memorySharer, job state.SnapshotReplicaJob) {
	result, err := shareSnapshotMemory(ctx, m.index, r.backend, job.StorageKey, job.LayerStorageKeys)
	if err != nil {
		r.log.Warn("snapshothipd: share snapshot memory pages", "snapshot_id", job.SnapshotID, "deployment_id", job.DeploymentID, "err", err)
		return
	}
	if !result.Skipped {
		r.log.Info("snapshothipd: snapshot memory shares its image blocks", "snapshot_id", job.SnapshotID, "deployment_id", job.DeploymentID, "shared_bytes", result.SharedBytes)
	}
}
