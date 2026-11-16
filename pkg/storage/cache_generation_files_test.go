package storage_test

// adr: 20261116190000 — each generation of a routed runtime base has its own
// cache file and marker, so a release pre-staging the next generation never
// replaces the file the serving vmmd boots, and the node need not drain.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// sideBySideCaches opens two caches on one node's cache root: the serving
// daemon of a release before ADR-20261116190000 (unrouted, so its file is the
// logical one) and a candidate that routes the base to its generation.
func sideBySideCaches(t *testing.T) (serving, candidate *storage.LocalCacheBackend) {
	t.Helper()
	dir := t.TempDir()
	shared, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "cache")
	serving, err = storage.NewLocalCacheBackend(remoteParent{shared}, root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = storage.NewLocalCacheBackend(remoteParent{shared}, root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := candidate.RouteGeneration(routedKey, generationKey); err != nil || !ok {
		t.Fatalf("RouteGeneration = %v, %v; want routed", ok, err)
	}
	return serving, candidate
}

func cachedFile(t *testing.T, c *storage.LocalCacheBackend, key string) (string, string) {
	t.Helper()
	path, ok, err := c.LocalPath(key)
	if err != nil || !ok {
		t.Fatalf("LocalPath(%s) = %v, %v; want a cached file", key, ok, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, string(body)
}

func cacheGeneration(t *testing.T, c *storage.LocalCacheBackend, key string) string {
	t.Helper()
	generation, ok, err := c.CachedGeneration(key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return generation
}

// The rc.290 mechanism (ADR-20261024085817): the candidate's pre-stage
// replaced the serving release's cached base and moved its marker, and the
// serving vmmd refused every boot. Now each generation keeps its own file.
func TestCandidateStagingLeavesTheServingGenerationsFile(t *testing.T) {
	serving, candidate := sideBySideCaches(t)
	if err := serving.Put(t.Context(), routedKey, strings.NewReader("serving generation")); err != nil {
		t.Fatal(err)
	}
	if err := serving.MarkGeneration(routedKey, "ref@old"); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Put(t.Context(), routedKey, strings.NewReader("candidate generation")); err != nil {
		t.Fatal(err)
	}
	if err := candidate.MarkGeneration(routedKey, "ref@new"); err != nil {
		t.Fatal(err)
	}

	servingPath, servingBody := cachedFile(t, serving, routedKey)
	candidatePath, candidateBody := cachedFile(t, candidate, routedKey)
	if servingPath == candidatePath {
		t.Fatalf("both generations share the cache file %s", servingPath)
	}
	if servingBody != "serving generation" || cacheGeneration(t, serving, routedKey) != "ref@old" {
		t.Fatalf("serving entry = %q marked %q; want its own generation untouched", servingBody, cacheGeneration(t, serving, routedKey))
	}
	if candidateBody != "candidate generation" || cacheGeneration(t, candidate, routedKey) != "ref@new" {
		t.Fatalf("candidate entry = %q marked %q; want the new generation", candidateBody, cacheGeneration(t, candidate, routedKey))
	}
	// Eviction and listings name the routed file by its generation key, so
	// a daemon without the route resolves the same file.
	entries, err := serving.LocalEntries()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	if got := strings.Join(keys, ","); !strings.Contains(got, generationKey) || !strings.Contains(got, routedKey) {
		t.Fatalf("cache entries = %s; want the logical and the generation file", got)
	}
}

// The first release with side-by-side files carries the file the node
// already holds over to its generation's name, without downloading it.
func TestAdoptCachedGenerationLinksTheLogicalFile(t *testing.T) {
	cases := []struct {
		name      string
		marked    string
		adopt     string
		wantAdopt bool
	}{
		{name: "the logical file holds the routed generation", marked: "ref@same", adopt: "ref@same", wantAdopt: true},
		{name: "the logical file holds another generation", marked: "ref@old", adopt: "ref@new"},
		{name: "the logical file has no marker", adopt: "ref@new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serving, candidate := sideBySideCaches(t)
			if err := serving.Put(t.Context(), routedKey, strings.NewReader("cached bytes")); err != nil {
				t.Fatal(err)
			}
			if tc.marked != "" {
				if err := serving.MarkGeneration(routedKey, tc.marked); err != nil {
					t.Fatal(err)
				}
			}
			adopted, err := candidate.AdoptCachedGeneration(routedKey, tc.adopt)
			if err != nil {
				t.Fatal(err)
			}
			if adopted != tc.wantAdopt {
				t.Fatalf("adopted = %v, want %v", adopted, tc.wantAdopt)
			}
			if !tc.wantAdopt {
				if got := cacheGeneration(t, candidate, routedKey); got != "" {
					t.Fatalf("candidate marker = %q, want none", got)
				}
				return
			}
			servingPath, _ := cachedFile(t, serving, routedKey)
			candidatePath, body := cachedFile(t, candidate, routedKey)
			if body != "cached bytes" || cacheGeneration(t, candidate, routedKey) != tc.adopt {
				t.Fatalf("adopted entry = %q marked %q", body, cacheGeneration(t, candidate, routedKey))
			}
			a, errA := os.Stat(servingPath)
			b, errB := os.Stat(candidatePath)
			if errA != nil || errB != nil || !os.SameFile(a, b) {
				t.Fatalf("adopted file is not a link of the logical file (%v, %v)", errA, errB)
			}
			if again, err := candidate.AdoptCachedGeneration(routedKey, tc.adopt); err != nil || again {
				t.Fatalf("second adopt = %v, %v; want a no-op", again, err)
			}
		})
	}
}

// A release that no longer boots a generation drops its pin; the entry stays
// until eviction, and the file its own release boots keeps its pin.
func TestUnpinCachedGenerationsKeepsTheEntries(t *testing.T) {
	serving, candidate := sideBySideCaches(t)
	if err := serving.Put(t.Context(), routedKey, strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	if err := serving.MarkGeneration(routedKey, "ref@old"); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Put(t.Context(), routedKey, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if err := candidate.MarkGeneration(routedKey, "ref@new"); err != nil {
		t.Fatal(err)
	}
	removed, err := candidate.UnpinCachedGenerations(func(key string) bool { return key == routedKey })
	if err != nil || removed != 1 {
		t.Fatalf("unpinned %d, %v; want 1", removed, err)
	}
	if got := cacheGeneration(t, serving, routedKey); got != "" {
		t.Fatalf("logical marker = %q, want removed", got)
	}
	if _, body := cachedFile(t, serving, routedKey); body != "old" {
		t.Fatalf("logical entry = %q, want it still cached", body)
	}
	if got := cacheGeneration(t, candidate, routedKey); got != "ref@new" {
		t.Fatalf("candidate marker = %q, want kept", got)
	}
}
