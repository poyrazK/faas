package fcvm

// adr: 20261026181418 — release N and N-1 both boot their own runtime base
// generation in a mixed compute fleet, whichever node stages last.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	mixedBaseKey = "base/base-amd64.ext4"
	// production-us 2026-10-26: rc.294 (N-1) and rc.296 (N) base-minimal.
	mixedGenN1 = "ghcr.io/poyrazk/base-minimal@sha256:19dd3482000000000000000000000000000000000000000000000000000000a1"
	mixedGenN  = "ghcr.io/poyrazk/base-minimal@sha256:296000000000000000000000000000000000000000000000000000000000000b"
)

// mixedGroupKeys is a base group in the order imaged publishes it: the scan
// sidecar, vmmd's commit marker, last.
func mixedGroupKeys(baseKey string) []string {
	return []string{baseKey, baseKey + ".digest", wire.ScanKeyForBaseKey(baseKey)}
}

// routeMixedGroup routes a node cache's base group to generation, as imaged
// and vmmd do at startup.
func routeMixedGroup(t *testing.T, cache *storage.LocalCacheBackend, generation string) {
	t.Helper()
	logical := mixedGroupKeys(mixedBaseKey)
	published := mixedGroupKeys(storagekey.BaseGeneration(mixedBaseKey, generation))
	for i := range logical {
		if ok, err := storage.RouteGeneration(cache, logical[i], published[i]); err != nil || !ok {
			t.Fatalf("route %s: %v, %v", logical[i], ok, err)
		}
	}
}

// publishMixed models one imaged staging generation through its node's cache.
// A routed writer is a release with generation keys; an unrouted one is a
// release before them, which writes the shared logical keys.
func publishMixed(t *testing.T, parent storage.StorageBackend, generation string, routed bool) {
	t.Helper()
	writer, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if routed {
		routeMixedGroup(t, writer, generation)
	}
	scan, _ := json.Marshal(map[string]any{"image": generation,
		"findings": map[string]int{"CRITICAL": 0}, "fix_available_findings": map[string]int{"CRITICAL": 0}})
	for i, body := range []string{"ext4 of " + generation, "digest of " + generation, string(scan)} {
		if err := writer.Put(t.Context(), mixedGroupKeys(mixedBaseKey)[i], strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
}

// mixedNode is one compute node's cache and the vmmd booting from it.
func mixedNode(t *testing.T, cache *storage.LocalCacheBackend, generation string) *Manager {
	t.Helper()
	routeMixedGroup(t, cache, generation)
	m := &Manager{}
	m.WithStorage(cache)
	m.WithBaseGenerations(map[string]string{mixedBaseKey: generation})
	return m
}

// bootMixed runs vmmd's admission gate and returns the base bytes the node
// would attach as drive0.
func bootMixed(ctx context.Context, m *Manager, cache *storage.LocalCacheBackend) (string, error) {
	if err := m.bringUpScanCheck(ctx, mixedBaseKey); err != nil {
		return "", err
	}
	rc, err := cache.Get(ctx, mixedBaseKey)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	return string(b), err
}

func TestMixedReleaseFleetBootsEachGeneration(t *testing.T) {
	type publication struct {
		generation string
		routed     bool
	}
	cases := []struct {
		name string
		// publications in order: imaged runs on N-1 and N nodes.
		published []publication
		// rebuildN leaves a rebuild of N in flight under N's own keys.
		rebuildN bool
		// boots names each node's generation and whether its vmmd boots it.
		boots map[string]bool
		// legacy is the generation the shared logical key must still name,
		// for a vmmd from before generation keys.
		legacy string
	}{
		// 16:53 fsn-2's rc.296 pre-stage publishes N; 17:03 fsn-3's rc.294
		// imaged restages base-minimal for a source build. With generation
		// keys, N's pre-stage never wrote the logical key, and N's vmmd
		// never reads it.
		{name: "2026-10-26: an N-1 imaged restages after N's pre-stage",
			published: []publication{{mixedGenN1, false}, {mixedGenN, true}, {mixedGenN1, false}},
			boots:     map[string]bool{mixedGenN: true, mixedGenN1: true}, legacy: mixedGenN1},
		{name: "N-1 also has generation keys and restages after N's pre-stage",
			published: []publication{{mixedGenN1, true}, {mixedGenN, true}, {mixedGenN1, true}},
			boots:     map[string]bool{mixedGenN: true, mixedGenN1: true}},
		{name: "N's pre-stage published last",
			published: []publication{{mixedGenN1, false}, {mixedGenN1, true}, {mixedGenN, true}},
			boots:     map[string]bool{mixedGenN: true, mixedGenN1: true}, legacy: mixedGenN1},
		// Expand/contract: N reads a generation that only a release before
		// generation keys published.
		{name: "N's generation was published only under the legacy key",
			published: []publication{{mixedGenN, false}},
			boots:     map[string]bool{mixedGenN: true}},
		// The incident without generation keys: both releases write the one
		// logical key, and the last writer decides what every node fetches.
		{name: "before generation keys: the last legacy writer wins",
			published: []publication{{mixedGenN, false}, {mixedGenN1, false}},
			boots:     map[string]bool{mixedGenN: false, mixedGenN1: true}, legacy: mixedGenN1},
		// A rebuild of N under its own keys refuses rather than adopt older
		// bytes from the legacy key; the next boot after it completes adopts.
		{name: "a rebuild of N is in flight under its own keys",
			published: []publication{{mixedGenN, false}, {mixedGenN, true}}, rebuildN: true,
			boots: map[string]bool{mixedGenN: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			parent := &memStorage{}
			for _, p := range tc.published {
				publishMixed(t, parent, p.generation, p.routed)
			}
			if tc.rebuildN {
				refusal := `{"image":"","findings":{"CRITICAL":9999},"fix_available_findings":{"CRITICAL":9999}}`
				key := wire.ScanKeyForBaseKey(storagekey.BaseGeneration(mixedBaseKey, mixedGenN))
				if err := parent.Put(ctx, key, strings.NewReader(refusal)); err != nil {
					t.Fatal(err)
				}
			}
			for generation, want := range tc.boots {
				cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				got, err := bootMixed(ctx, mixedNode(t, cache, generation), cache)
				if !want {
					if err == nil {
						t.Fatalf("node on %s booted %q, want a refusal", generation, got)
					}
					continue
				}
				if err != nil || got != "ext4 of "+generation {
					t.Fatalf("node on %s: drive0 %q, %v; want its own generation", generation, got, err)
				}
			}
			if tc.legacy != "" {
				rc, err := parent.Get(ctx, wire.ScanKeyForBaseKey(mixedBaseKey))
				if err != nil {
					t.Fatal(err)
				}
				var scan baseScanSidecar
				_ = json.NewDecoder(rc).Decode(&scan)
				_ = rc.Close()
				if scan.Image != tc.legacy {
					t.Fatalf("shared logical scan names %q, want %q for a vmmd before generation keys", scan.Image, tc.legacy)
				}
			}
		})
	}
}

// A compute rollback across a generation change: the node's cache commits N,
// and the rolled-back vmmd is configured for N-1 again. It adopts N-1 from
// N-1's own keys, and rolling forward again adopts N.
func TestRollbackAcrossAGenerationChangeAdoptsTheOlderGeneration(t *testing.T) {
	ctx := t.Context()
	parent := &memStorage{}
	publishMixed(t, parent, mixedGenN1, true)
	publishMixed(t, parent, mixedGenN, true)
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, generation := range []string{mixedGenN, mixedGenN1, mixedGenN} {
		got, err := bootMixed(ctx, mixedNode(t, cache, generation), cache)
		if err != nil || got != "ext4 of "+generation {
			t.Fatalf("vmmd on %s: drive0 %q, %v; want its own generation", generation, got, err)
		}
		if marked, ok, err := cache.CachedGeneration(mixedBaseKey); err != nil || !ok || marked != generation {
			t.Fatalf("cache marker = %q, %v, %v; want %s", marked, ok, err, generation)
		}
	}
}
