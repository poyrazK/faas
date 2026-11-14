package state

// adr: 20261114120000

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// NodeCacheRetention is what a compute node's artifact cache must keep: the
// snapshot captures rows still reference and the runtime release bases a
// deployment or an upgrade still uses.
type NodeCacheRetention struct {
	// Captures maps the capture prefix (the memory key without its last
	// element, e.g. snap/<deployment>/captures/<capture>/v2/) of every
	// referenced capture to its strongest use.
	Captures map[string]CaptureUse
	// RuntimeReleases maps a runtime release base key to the release.
	RuntimeReleases map[string]RuntimeReleaseRetention
}

// CaptureUse is why a capture is kept, weakest first.
type CaptureUse int

const (
	// CaptureStale is a stale snapshot's capture: kept until imaged deletes
	// the row, but it can no longer be restored.
	CaptureStale CaptureUse = iota + 1
	// CaptureOther is a branch capture, a crash capture or an in-flight live
	// migration.
	CaptureOther
	// CaptureLive is a non-stale snapshot's capture.
	CaptureLive
)

func captureUse(s string) CaptureUse {
	switch s {
	case "live":
		return CaptureLive
	case "stale":
		return CaptureStale
	default:
		return CaptureOther
	}
}

// RuntimeReleaseRetention is one runtime release as the node cache sees it.
type RuntimeReleaseRetention struct {
	CreatedAt time.Time
	// Keep is true while a deployment that is not failed or cancelled is
	// bound to the release, or a runtime upgrade targets it.
	Keep bool
}

// NodeCacheRetentionReader is implemented by PgStore and MemStore. vmmd's
// snapshothipd sweep reads it to decide what its node cache may drop.
type NodeCacheRetentionReader interface {
	NodeCacheRetention(ctx context.Context) (NodeCacheRetention, error)
}

// SnapshotCapturePrefix is key without its last path element: every object of
// one capture (mem, vmstate, drive, backing) shares it. Empty for keys that
// are not objects of an immutable capture (legacy mutable pairs, other
// prefixes): the prefix's memory key must be a capture key.
func SnapshotCapturePrefix(key string) string {
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return ""
	}
	prefix := key[:i+1]
	if !IsSnapshotCaptureKey(prefix + "mem") {
		return ""
	}
	return prefix
}

// NodeCacheRetention reads the references from the database.
func (s *PgStore) NodeCacheRetention(ctx context.Context) (NodeCacheRetention, error) {
	q := sqlc.New()
	keys, err := q.ListReferencedSnapshotCaptureKeys(ctx, s.pool)
	if err != nil {
		return NodeCacheRetention{}, fmt.Errorf("state: list referenced snapshot captures: %w", err)
	}
	releases, err := q.ListRuntimeReleaseRetention(ctx, s.pool)
	if err != nil {
		return NodeCacheRetention{}, fmt.Errorf("state: list runtime release retention: %w", err)
	}
	out := NodeCacheRetention{Captures: map[string]CaptureUse{}, RuntimeReleases: map[string]RuntimeReleaseRetention{}}
	for _, k := range keys {
		out.addCapture(k.Key, captureUse(k.Use))
	}
	for _, r := range releases {
		key := RuntimeRelease{ID: r.ID, Runtime: r.Runtime, Architecture: r.Architecture}.BaseKey()
		out.RuntimeReleases[key] = RuntimeReleaseRetention{CreatedAt: r.CreatedAt.Time, Keep: r.Keep}
	}
	return out, nil
}

// NodeCacheRetention is the in-memory equivalent: snapshots from the
// snapshot list, and every runtime release bound to an artifact kept.
func (m *MemStore) NodeCacheRetention(_ context.Context) (NodeCacheRetention, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := NodeCacheRetention{Captures: map[string]CaptureUse{}, RuntimeReleases: map[string]RuntimeReleaseRetention{}}
	for _, s := range m.snapshots {
		use := CaptureLive
		if s.Stale {
			use = CaptureStale
		}
		out.addCapture(s.StorageKey, use)
	}
	bound := map[string]bool{}
	for _, id := range m.runtimeArtifactBindings {
		bound[id] = true
	}
	for _, r := range m.runtimeReleases {
		out.RuntimeReleases[r.BaseKey()] = RuntimeReleaseRetention{CreatedAt: r.CreatedAt, Keep: bound[r.ID]}
	}
	return out, nil
}

func (r *NodeCacheRetention) addCapture(key string, use CaptureUse) {
	prefix := SnapshotCapturePrefix(key)
	if prefix == "" {
		return
	}
	if use > r.Captures[prefix] {
		r.Captures[prefix] = use
	}
}
