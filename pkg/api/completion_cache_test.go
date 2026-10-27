// completion_cache_test.go — Tier A8 / ADR-083.
//
// Round-trip + TTL + corrupt-fallback tests for CompletionCache.
// Mirrors the pgtest pattern (no global state, t.TempDir() for the
// cache file path via SetPath). The cache itself has no global
// state, so parallel tests are safe.

package api

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newTestCache returns a CompletionCache rooted at a t.TempDir()
// subdirectory, with the mtime clock injected so TTL tests can
// pin "now" without sleeping.
func newTestCache(t *testing.T) *CompletionCache {
	t.Helper()
	dir := t.TempDir()
	c := NewCompletionCache()
	c.SetPath(filepath.Join(dir, "completion-cache.json"))
	c.ttl = time.Hour // tighter for tests; the 24h default is fine but
	// tests don't want to wait a day to verify IsFresh.
	return c
}

func TestCompletionCache_RoundTrip(t *testing.T) {
	c := newTestCache(t)
	in := CompletionCacheEntry{
		Apps: []CompletionCacheRecord{
			{ID: "id-app-1", Slug: "demo", Name: "demo"},
			{ID: "id-app-2", Slug: "staging-api", Name: "Staging API"},
		},
		Orgs: []CompletionCacheRecord{
			{ID: "id-org-1", Slug: "acme", Name: "Acme Corp"},
		},
	}
	if err := c.WriteEntry(in); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	out, mtime, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mtime.IsZero() {
		t.Fatalf("Read returned zero mtime; file should exist on disk")
	}
	if len(out.Apps) != 2 || out.Apps[0].Slug != "demo" {
		t.Fatalf("Apps round-trip mismatch: %+v", out.Apps)
	}
	if len(out.Orgs) != 1 || out.Orgs[0].Slug != "acme" {
		t.Fatalf("Orgs round-trip mismatch: %+v", out.Orgs)
	}
	if out.Version != completionCacheVersion {
		t.Fatalf("Version: got %d want %d", out.Version, completionCacheVersion)
	}
}

func TestCompletionCache_ReadMissingReturnsEmpty(t *testing.T) {
	c := newTestCache(t)
	out, mtime, err := c.Read()
	if err != nil {
		t.Fatalf("Read on missing file should be (zero, zero, nil); got %v", err)
	}
	if !mtime.IsZero() {
		t.Fatalf("Read on missing file should return zero mtime; got %v", mtime)
	}
	if out.Version != 0 || len(out.Apps) != 0 || len(out.Orgs) != 0 {
		t.Fatalf("Read on missing file should return zero entry; got %+v", out)
	}
}

func TestCompletionCache_ReadCorruptReturnsEmpty(t *testing.T) {
	c := newTestCache(t)
	if err := os.WriteFile(c.Path(), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seed corrupt cache: %v", err)
	}
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read on corrupt file should swallow error; got %v", err)
	}
	if len(out.Apps) != 0 || len(out.Orgs) != 0 {
		t.Fatalf("Read on corrupt file should return zero entry; got %+v", out)
	}
}

func TestCompletionCache_IsFresh(t *testing.T) {
	c := newTestCache(t)
	c.ttl = time.Minute
	// No file → not fresh.
	if c.IsFresh(time.Time{}) {
		t.Fatalf("IsFresh on zero mtime should be false")
	}
	// Write → fresh.
	if err := c.WriteEntry(CompletionCacheEntry{
		Apps: []CompletionCacheRecord{{Slug: "demo"}},
	}); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	_, mtime, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !c.IsFresh(mtime) {
		t.Fatalf("IsFresh on fresh file should be true")
	}
	// Backdate the mtime to be older than the TTL.
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(c.Path(), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	_, mtime, err = c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.IsFresh(mtime) {
		t.Fatalf("IsFresh on stale file should be false")
	}
}

func TestCompletionCache_MaybeRefresh_Apps(t *testing.T) {
	c := newTestCache(t)
	body := []byte(`[{"id":"id-1","slug":"demo","name":"demo"},{"id":"id-2","slug":"api","name":"API"}]`)
	c.MaybeRefresh("/v1/apps", body)
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Apps) != 2 || out.Apps[0].Slug != "demo" {
		t.Fatalf("Apps not refreshed: %+v", out.Apps)
	}
}

func TestCompletionCache_MaybeRefresh_OrgsEnvelope(t *testing.T) {
	c := newTestCache(t)
	body := []byte(`{"orgs":[{"id":"id-1","slug":"acme","name":"Acme"}]}`)
	c.MaybeRefresh("/v1/orgs", body)
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Orgs) != 1 || out.Orgs[0].Slug != "acme" {
		t.Fatalf("Orgs not refreshed: %+v", out.Orgs)
	}
}

func TestCompletionCache_MaybeRefresh_PreservesOther(t *testing.T) {
	c := newTestCache(t)
	// Seed apps first.
	c.MaybeRefresh("/v1/apps", []byte(`[{"id":"id-a","slug":"demo","name":"demo"}]`))
	// Then orgs — apps must survive.
	c.MaybeRefresh("/v1/orgs", []byte(`{"orgs":[{"id":"id-o","slug":"acme","name":"Acme"}]}`))
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Apps) != 1 || out.Apps[0].Slug != "demo" {
		t.Fatalf("Apps lost after orgs refresh: %+v", out.Apps)
	}
	if len(out.Orgs) != 1 || out.Orgs[0].Slug != "acme" {
		t.Fatalf("Orgs not refreshed: %+v", out.Orgs)
	}
}

// adr: 20261023115741 — the capabilities hidden for the account ride the
// same cache, and an empty answer is recorded as "nothing hidden".
func TestCompletionCache_MaybeRefresh_Capabilities(t *testing.T) {
	c := newTestCache(t)
	if _, _, ok := c.HiddenCapabilities(); ok {
		t.Fatal("a cache that never read capabilities must say so")
	}
	c.MaybeRefresh("/v1/apps", []byte(`[{"id":"id-a","slug":"demo","name":"demo"}]`))
	c.MaybeRefresh("/v1/capabilities", []byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":["realtime","queues"]}`))
	hidden, savedAt, ok := c.HiddenCapabilities()
	if !ok || savedAt.IsZero() || len(hidden) != 2 || hidden[0] != "realtime" {
		t.Fatalf("HiddenCapabilities = %v %v %v", hidden, savedAt, ok)
	}
	if out, _, _ := c.Read(); len(out.Apps) != 1 {
		t.Fatalf("apps lost after a capabilities refresh: %+v", out.Apps)
	}
	c.MaybeRefresh("/v1/capabilities", []byte(`{"registry_version":1,"plan":"pro","capabilities":[]}`))
	if hidden, _, ok := c.HiddenCapabilities(); !ok || len(hidden) != 0 {
		t.Fatalf("an answer without hidden capabilities = %v %v, want read and empty", hidden, ok)
	}
}

func TestCompletionCache_MaybeRefresh_IgnoresUnknownPath(t *testing.T) {
	c := newTestCache(t)
	c.MaybeRefresh("/v1/something-else", []byte(`[{"slug":"x"}]`))
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Apps) != 0 || len(out.Orgs) != 0 {
		t.Fatalf("Unknown path should not populate cache; got %+v", out)
	}
}

func TestCompletionCache_MaybeRefresh_SkipsEmptyBody(t *testing.T) {
	c := newTestCache(t)
	c.MaybeRefresh("/v1/apps", nil)
	c.MaybeRefresh("/v1/apps", []byte{})
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Apps) != 0 {
		t.Fatalf("Empty body should not populate cache; got %+v", out.Apps)
	}
}

func TestCompletionCache_ConcurrentWritesAreSafe(t *testing.T) {
	c := newTestCache(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.MaybeRefresh("/v1/apps", []byte(`[{"id":"id","slug":"demo","name":"demo"}]`))
		}(i)
	}
	wg.Wait()
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read after concurrent writes: %v", err)
	}
	if len(out.Apps) != 1 {
		t.Fatalf("Concurrent writes should converge to one record; got %d", len(out.Apps))
	}
}

// TestCompletionCache_CrossPathRacePreservesBothFields exercises the
// read-modify-write race that the outer c.mu lock in MaybeRefresh
// was added to fix: concurrent /v1/apps + /v1/orgs refreshes must
// NOT clobber each other's field. Without the outer lock, both
// goroutines read the same baseline, both write — last writer wins
// and one field is wiped.
func TestCompletionCache_CrossPathRacePreservesBothFields(t *testing.T) {
	c := newTestCache(t)
	var wg sync.WaitGroup
	// Fire a few interleaved apps+orgs refreshes. The exact count
	// is not load-bearing — the bug surfaces with even two racing
	// goroutines on different paths; we use a handful to make the
	// failure obvious under -race. Higher counts make the test
	// slow on CI (atomic rename is the bottleneck, not the mutex).
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.MaybeRefresh("/v1/apps", []byte(`[{"id":"id-a","slug":"demo","name":"demo"}]`))
		}()
		go func() {
			defer wg.Done()
			c.MaybeRefresh("/v1/orgs", []byte(`{"orgs":[{"id":"id-o","slug":"acme","name":"Acme"}]}`))
		}()
	}
	wg.Wait()
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read after cross-path race: %v", err)
	}
	if len(out.Apps) != 1 || out.Apps[0].Slug != "demo" {
		t.Fatalf("Apps lost under cross-path race: %+v", out.Apps)
	}
	if len(out.Orgs) != 1 || out.Orgs[0].Slug != "acme" {
		t.Fatalf("Orgs lost under cross-path race: %+v", out.Orgs)
	}
}

func TestCompletionCache_WriteIsAtomic(t *testing.T) {
	c := newTestCache(t)
	if err := c.WriteEntry(CompletionCacheEntry{
		Apps: []CompletionCacheRecord{{Slug: "first"}},
	}); err != nil {
		t.Fatalf("WriteEntry first: %v", err)
	}
	if err := c.WriteEntry(CompletionCacheEntry{
		Apps: []CompletionCacheRecord{{Slug: "second"}},
	}); err != nil {
		t.Fatalf("WriteEntry second: %v", err)
	}
	out, _, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out.Apps) != 1 || out.Apps[0].Slug != "second" {
		t.Fatalf("Second write should fully replace first; got %+v", out.Apps)
	}
	// No leftover tmp files in the dir.
	dir := filepath.Dir(c.Path())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("Leftover tmp file: %s", e.Name())
		}
	}
}

func TestCompletionCache_FileModeIs0600(t *testing.T) {
	c := newTestCache(t)
	if err := c.WriteEntry(CompletionCacheEntry{
		Apps: []CompletionCacheRecord{{Slug: "demo"}},
	}); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	st, err := os.Stat(c.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("File mode: got %o want 0600", got)
	}
}

func TestCompletionCache_DirModeIs0700(t *testing.T) {
	c := newTestCache(t)
	if err := c.WriteEntry(CompletionCacheEntry{}); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	st, err := os.Stat(filepath.Dir(c.Path()))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o700 {
		t.Fatalf("Dir mode: got %o want 0700", got)
	}
}
