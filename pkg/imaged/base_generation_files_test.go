package imaged

// adr: 20261116190000 — one node holds runtime base generations side by
// side: the first release with per-generation cache files carries the
// node's file over without a download, and vmmd's startup unpins only the
// generations its release no longer boots.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func putMarkedGroup(t *testing.T, cache *storage.LocalCacheBackend, baseKey, body, generation string) {
	t.Helper()
	for _, key := range []string{baseKey + ".digest", wire.ScanKeyForBaseKey(baseKey), baseKey} {
		if err := cache.Put(t.Context(), key, strings.NewReader(body+" "+key)); err != nil {
			t.Fatal(err)
		}
	}
	if err := markCachedBaseGeneration(t.Context(), cache, baseKey, baseKey+".digest", generation); err != nil {
		t.Fatal(err)
	}
}

func markerOf(t *testing.T, cache *storage.LocalCacheBackend, key string) string {
	t.Helper()
	generation, ok, err := cache.CachedGeneration(key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return generation
}

func TestSideBySideBaseGenerationsOnOneNode(t *testing.T) {
	const (
		baseKey = "base/base-amd64.ext4"
		older   = "ghcr.io/example/minimal@sha256:1111"
		newer   = "ghcr.io/example/minimal@sha256:2222"
		kernel  = "kernels/vmlinux-test"
	)
	fleet := newMixedFleet(t)

	// A release before ADR-20261116190000 cached its generation under the
	// logical names; an unrelated pinned artifact shares the cache.
	legacy := fleet.node("n")
	putMarkedGroup(t, legacy, baseKey, "older", older)
	if err := legacy.Put(t.Context(), kernel, strings.NewReader("kernel")); err != nil {
		t.Fatal(err)
	}
	if err := legacy.MarkGeneration(kernel, "sha256:kernel"); err != nil {
		t.Fatal(err)
	}

	// The first release with side-by-side files, same generation: routing
	// carries the cached group over by link.
	same := fleet.node("n")
	if _, err := RouteBaseGenerations(same, map[string]string{baseKey: older}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{baseKey, baseKey + ".digest", wire.ScanKeyForBaseKey(baseKey)} {
		if got := markerOf(t, same, key); got != older {
			t.Fatalf("carried-over %s marker = %q, want %q", key, got, older)
		}
	}

	// The next release pre-stages another generation beside it: the file
	// the serving vmmd boots keeps its bytes and marker.
	next := fleet.node("n")
	routed, err := RouteBaseGenerations(next, map[string]string{baseKey: newer})
	if err != nil || len(routed) != 1 {
		t.Fatalf("routed %q, %v", routed, err)
	}
	putMarkedGroup(t, next, baseKey, "newer", newer)
	if got := markerOf(t, same, baseKey); got != older {
		t.Fatalf("serving generation marker = %q after the pre-stage, want %q", got, older)
	}
	if got := markerOf(t, next, baseKey); got != newer {
		t.Fatalf("pre-staged generation marker = %q, want %q", got, newer)
	}

	// The new vmmd starts: the generations it does not boot lose their pin,
	// its own and unrelated artifacts keep theirs.
	unpinned, err := UnpinOtherBaseGenerations(next, map[string]string{baseKey: newer}, routed)
	if err != nil {
		t.Fatal(err)
	}
	if unpinned != 6 {
		t.Fatalf("unpinned %d entries, want the logical and the older generation's three each", unpinned)
	}
	if got := markerOf(t, legacy, baseKey); got != "" {
		t.Fatalf("logical marker = %q, want unpinned", got)
	}
	if got := markerOf(t, same, baseKey); got != "" {
		t.Fatalf("older generation marker = %q, want unpinned", got)
	}
	if got := markerOf(t, next, baseKey); got != newer {
		t.Fatalf("booted generation marker = %q, want kept", got)
	}
	if got := markerOf(t, legacy, kernel); got != "sha256:kernel" {
		t.Fatalf("unrelated artifact marker = %q, want kept", got)
	}
}

// A node that joins a fleet whose shared store already holds a base group
// marks it without having read every sidecar first: marking reads the
// missing ones through (a fresh imaged exited on the digest sidecar).
func TestMarkCachedBaseGenerationFillsAFreshNodeCache(t *testing.T) {
	const (
		baseKey    = "base/base-amd64.ext4"
		generation = "ghcr.io/example/minimal@sha256:1111"
	)
	fleet := newMixedFleet(t)
	putMarkedGroup(t, fleet.node("first"), baseKey, "group", generation)

	fresh := fleet.node("fresh")
	if err := markCachedBaseGeneration(t.Context(), fresh, baseKey, baseKey+".digest", generation); err != nil {
		t.Fatalf("fresh node: %v", err)
	}
	for _, key := range []string{baseKey + ".digest", wire.ScanKeyForBaseKey(baseKey), baseKey} {
		if got := markerOf(t, fresh, key); got != generation {
			t.Errorf("%s generation = %q, want %q", key, got, generation)
		}
	}
}
