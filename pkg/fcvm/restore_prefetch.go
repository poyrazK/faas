package fcvm

import (
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
)

// Restore working-set prefetch (ADR-224).
//
// A restore demand-faults the guest's memory from the snapshot's mem file.
// When those pages have left the host page cache — the normal state on a
// node whose parks keep writing new snapshots — every fault is a synchronous
// disk read on a paused vCPU, and resume_hook alone grows from ~32 ms to
// ~100 ms. After a restore reaches readiness, vmmd reads the Firecracker
// process's page table for the mem-file mapping (the exact pages the guest
// touched), and remembers those file ranges per snapshot family. The next
// wake of the same family issues asynchronous readahead for them before the
// lease, network and restore gate are prepared, so the reads overlap work
// the wake was doing anyway.
//
// Everything here is advisory. A missing set, a failed read of /proc, or a
// failed fadvise changes only latency, never what is restored.

const (
	// restorePrefetchMaxFamilies bounds the in-memory store. A set is a few
	// hundred ranges, so the worst case is a few MiB of vmmd heap.
	restorePrefetchMaxFamilies = 2048
	// restorePrefetchMaxRanges bounds one set; gaps are widened until the
	// set fits so a fragmented working set still prefetches as a whole.
	restorePrefetchMaxRanges = 1024
	// restorePrefetchMaxBytes caps one prefetch. Blind prefetch measured a
	// net loss (ADR-192), so a recorded set larger than this is truncated
	// rather than turned into a bulk read.
	restorePrefetchMaxBytes = 256 << 20
	// restorePrefetchMergeGap joins ranges separated by less than this; one
	// larger read is cheaper than two small ones on network block storage.
	restorePrefetchMergeGap = 128 << 10
)

// fileRange is a byte range of a snapshot mem file.
type fileRange struct {
	Off int64
	Len int64
}

type restorePrefetchSet struct {
	ranges []fileRange
	bytes  int64
}

// restorePrefetchStore remembers the last recorded working set per snapshot
// family. It is node-local and deliberately not persisted: a restarted vmmd
// relearns each family on its next restore.
type restorePrefetchStore struct {
	mu    sync.Mutex
	sets  map[string]restorePrefetchSet
	order []string // insertion order, oldest first, for bounded eviction
}

func newRestorePrefetchStore() *restorePrefetchStore {
	return &restorePrefetchStore{sets: make(map[string]restorePrefetchSet)}
}

func (s *restorePrefetchStore) get(family string) (restorePrefetchSet, bool) {
	if s == nil || family == "" {
		return restorePrefetchSet{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, ok := s.sets[family]
	return set, ok
}

func (s *restorePrefetchStore) put(family string, set restorePrefetchSet) {
	if s == nil || family == "" || len(set.ranges) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sets[family]; !ok {
		s.order = append(s.order, family)
		for len(s.order) > restorePrefetchMaxFamilies {
			delete(s.sets, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.sets[family] = set
}

// snapshotPrefetchFamily groups every capture of one deployment:
// snap/<id>/captures/<capture>/v2/mem → snap/<id>. Consecutive captures of
// the same app share guest-physical layout closely enough that the previous
// capture's working set is a good prefetch for a new one. Legacy keys are
// their own family.
func snapshotPrefetchFamily(storageKey string) string {
	if i := strings.Index(storageKey, "/captures/"); i > 0 && strings.HasPrefix(storageKey, "snap/") {
		return storageKey[:i]
	}
	return storageKey
}

// coalesceFileRanges sorts ranges, merges any closer than gap, widens the gap
// until at most maxRanges remain, and truncates the result at maxBytes.
func coalesceFileRanges(in []fileRange, gap int64, maxRanges int, maxBytes int64) restorePrefetchSet {
	if len(in) == 0 {
		return restorePrefetchSet{}
	}
	if gap <= 0 {
		gap = 4096
	}
	sorted := append([]fileRange(nil), in...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Off < sorted[j].Off })
	var out []fileRange
	for {
		out = out[:0]
		for _, r := range sorted {
			if r.Len <= 0 {
				continue
			}
			if n := len(out); n > 0 && r.Off <= out[n-1].Off+out[n-1].Len+gap {
				if end := r.Off + r.Len; end > out[n-1].Off+out[n-1].Len {
					out[n-1].Len = end - out[n-1].Off
				}
				continue
			}
			out = append(out, r)
		}
		if len(out) <= maxRanges || gap >= 1<<30 {
			break
		}
		gap *= 2
	}
	set := restorePrefetchSet{}
	for _, r := range out {
		if set.bytes+r.Len > maxBytes {
			r.Len = maxBytes - set.bytes
		}
		if r.Len <= 0 {
			break
		}
		set.ranges = append(set.ranges, r)
		set.bytes += r.Len
	}
	return set
}

// restorePrefetcher is implemented by VMMs that can warm a snapshot's
// working set ahead of Restore. Manager.Wake calls it before any other wake
// work so the reads overlap lease, network and jail preparation.
type restorePrefetcher interface {
	PrefetchRestore(storageKey string) int64
}

// WithRestorePrefetch enables or disables the ADR-224 working-set prefetch.
// It is enabled by default; disabling also stops recording.
func (v *JailerVMM) WithRestorePrefetch(enabled bool) *JailerVMM {
	if enabled {
		if v.restorePrefetch == nil {
			v.restorePrefetch = newRestorePrefetchStore()
		}
		return v
	}
	v.restorePrefetch = nil
	return v
}

// PrefetchRestore starts asynchronous readahead of the recorded working set
// for storageKey's snapshot family and returns the number of bytes requested
// (0 when there is nothing to do). It never blocks on I/O.
func (v *JailerVMM) PrefetchRestore(storageKey string) int64 {
	if v == nil || v.restorePrefetch == nil || storageKey == "" {
		return 0
	}
	set, ok := v.restorePrefetch.get(snapshotPrefetchFamily(storageKey))
	if !ok {
		return 0
	}
	path, _, local, err := v.probeRestoreLocalPath(storageKey)
	if err != nil || !local || path == "" {
		// A remote snapshot is materialised by a full streamed copy, which
		// leaves it resident; prefetch only helps a local cached file.
		return 0
	}
	if _, err := os.Stat(path); err != nil {
		// Local backends map a key to a path without checking it exists; do
		// not report a prefetch for a file that is not there.
		return 0
	}
	go func() {
		if err := adviseWillNeed(path, set.ranges); err != nil {
			slog.Default().Debug("restore prefetch failed", "storage_key", storageKey, "err", err)
		}
	}()
	return set.bytes
}

// recordRestoreWorkingSet captures, off the wake's critical path, the mem-file
// ranges the restored guest touched up to readiness.
func (v *JailerVMM) recordRestoreWorkingSet(instance, storageKey, memPath string) {
	if v == nil || v.restorePrefetch == nil || storageKey == "" || memPath == "" {
		return
	}
	pid, ok := v.InstancePID(instance)
	if !ok {
		return
	}
	store := v.restorePrefetch
	go func() {
		touched, err := touchedFileRanges(pid, memPath)
		if err != nil {
			slog.Default().Debug("restore working set not recorded", "instance", instance, "err", err)
			return
		}
		set := coalesceFileRanges(touched, restorePrefetchMergeGap, restorePrefetchMaxRanges, restorePrefetchMaxBytes)
		store.put(snapshotPrefetchFamily(storageKey), set)
		slog.Default().Debug("restore working set recorded", "instance", instance,
			"ranges", len(set.ranges), "bytes", set.bytes)
	}()
}
