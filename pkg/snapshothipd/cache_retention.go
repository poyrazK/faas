package snapshothipd

// adr: 20261114120000
//
// Node cache retention. imaged deletes a snapshot's row and its shared-storage
// objects, but every compute node that cached the capture kept its copy until
// the cache budget pushed it out: 171 memory files (10 GB of their own blocks)
// on one production node, all of deleted snapshots. Runtime release bases were
// never pushed out at all, because reads keep them young (67 cached, 35 bound
// to live deployments). Now that the budget comes from the disk, the budget
// rarely evicts, so the node removes what nothing needs on purpose, and tells
// the cache what to evict first and last when the disk does fill.

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const runtimeReleaseKeyPrefix = "base/releases/runner-"

// WithCacheRetention lets the runner sweep the node cache every
// api.NodeCacheSweepInterval with what reader says rows still reference, and
// order eviction by it. nil leaves the cache to plain oldest-first eviction.
func (r *Runner) WithCacheRetention(reader state.NodeCacheRetentionReader) *Runner {
	r.retention = reader
	return r
}

// cacheRetentionDecision classifies cache keys against one read of the
// references.
type cacheRetentionDecision struct {
	ret state.NodeCacheRetention
	now time.Time
}

// tier is the eviction tier of key: a live snapshot's capture last, a stale
// snapshot's capture and an unused runtime release first. A capture no row
// references yet stays in the normal tier: it may have been cached after the
// references were read, and the sweep removes it once it is old enough.
func (d cacheRetentionDecision) tier(key string) int {
	if prefix := state.SnapshotCapturePrefix(key); prefix != "" {
		switch d.ret.Captures[prefix] {
		case state.CaptureLive:
			return storage.EvictLast
		case state.CaptureStale:
			return storage.EvictFirst
		default:
			return storage.EvictNormal
		}
	}
	if d.releaseUnused(key) {
		return storage.EvictFirst
	}
	return storage.EvictNormal
}

// releaseUnused reports whether key is a runtime release base that no
// deployment or upgrade uses and that is past its grace.
func (d cacheRetentionDecision) releaseUnused(key string) bool {
	rel, known := d.ret.RuntimeReleases[key]
	return known && !rel.Keep && d.now.Sub(rel.CreatedAt) > api.RuntimeReleaseCacheGrace
}

// removable reports whether the sweep deletes e from this node's cache: a
// capture no row references, cached longer than the orphan grace; a runtime
// release base nothing uses; or a runtime release base the database does not
// know, unread for the release grace.
func (d cacheRetentionDecision) removable(e storage.CachedEntry) bool {
	if prefix := state.SnapshotCapturePrefix(e.Key); prefix != "" {
		_, referenced := d.ret.Captures[prefix]
		return !referenced && d.now.Sub(e.ModTime) > api.SnapshotCacheOrphanGrace
	}
	if !strings.HasPrefix(e.Key, runtimeReleaseKeyPrefix) {
		return false
	}
	if _, known := d.ret.RuntimeReleases[e.Key]; known {
		return d.releaseUnused(e.Key)
	}
	return d.now.Sub(e.ModTime) > api.RuntimeReleaseCacheGrace
}

// runCacheRetention sweeps once a minute after start, then every
// api.NodeCacheSweepInterval, until ctx ends.
func (r *Runner) runCacheRetention(ctx context.Context) {
	if r.retention == nil {
		return
	}
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.sweepCache(ctx, time.Now())
		t.Reset(api.NodeCacheSweepInterval)
	}
}

// retentionCaches returns the distinct caches that hold snapshots and
// runtime release bases.
func (r *Runner) retentionCaches() []*storage.LocalCacheBackend {
	var out []*storage.LocalCacheBackend
	for _, probe := range []string{"snap/retention", runtimeReleaseKeyPrefix + "retention"} {
		cache, _, err := storage.CacheBackendForKey(r.backend, probe)
		if err != nil || cache == nil {
			continue
		}
		seen := false
		for _, c := range out {
			seen = seen || c == cache
		}
		if !seen {
			out = append(out, cache)
		}
	}
	return out
}

// sweepCache reads the references once, orders eviction by them and removes
// what nothing needs. A failed read changes nothing.
func (r *Runner) sweepCache(ctx context.Context, now time.Time) {
	ret, err := r.retention.NodeCacheRetention(ctx)
	if err != nil {
		r.log.Warn("snapshothipd: node cache retention read failed; nothing removed", "node_id", r.nodeID, "err", err)
		return
	}
	d := cacheRetentionDecision{ret: ret, now: now}
	for _, cache := range r.retentionCaches() {
		cache.SetEvictionTier(d.tier)
		entries, err := cache.LocalEntries()
		if err != nil {
			r.log.Warn("snapshothipd: node cache list failed", "node_id", r.nodeID, "err", err)
			continue
		}
		var captures, releases int
		var bytes int64
		for _, e := range entries {
			if ctx.Err() != nil {
				return
			}
			if !d.removable(e) {
				continue
			}
			if err := cache.EvictLocal(e.Key); err != nil {
				r.log.Warn("snapshothipd: node cache removal failed", "node_id", r.nodeID, "key", e.Key, "err", err)
				continue
			}
			bytes += e.AllocatedBytes
			if strings.HasPrefix(e.Key, "snap/") {
				captures++
			} else {
				releases++
			}
		}
		if captures+releases > 0 {
			r.log.Info("snapshothipd: node cache swept", "node_id", r.nodeID,
				"snapshot_objects", captures, "runtime_release_bases", releases, "bytes", bytes)
		}
	}
}
