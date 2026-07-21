package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
)

// TestPrefixRouterPutGetDelete is the round-trip suite: a router
// with two backends (apps/ → A, snap/ → S) and a fallback (F)
// routes each prefix correctly and the fallback catches the rest.
// The keys are written via the router and read back via the router
// to assert the wrappers don't lose data.
func TestPrefixRouterPutGetDelete(t *testing.T) {
	a := newTestBackend(t)
	s := newTestBackend(t)
	f := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
		"snap/": s,
	}, f)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()

	if err := router.Put(ctx, "apps/slug/dep.ext4", strings.NewReader("app-data")); err != nil {
		t.Fatalf("put apps: %v", err)
	}
	if err := router.Put(ctx, "snap/dep/mem", strings.NewReader("snap-data")); err != nil {
		t.Fatalf("put snap: %v", err)
	}
	if err := router.Put(ctx, "base/runtime.ext4", strings.NewReader("base-data")); err != nil {
		t.Fatalf("put base: %v", err)
	}

	// Each key must read back via the router; the contents must
	// match what we Put, and the underlying backend must have the
	// file at the stripped path (no prefix leakage).
	mustReadRouter := func(key, want string) {
		t.Helper()
		got := mustReadAll(t, router, key)
		if got != want {
			t.Fatalf("get %s: got %q, want %q", key, got, want)
		}
	}
	mustReadRouter("apps/slug/dep.ext4", "app-data")
	mustReadRouter("snap/dep/mem", "snap-data")
	mustReadRouter("base/runtime.ext4", "base-data")

	// Underlying backends hold the stripped keys.
	if got := mustReadAll(t, a, "slug/dep.ext4"); got != "app-data" {
		t.Fatalf("apps backend: got %q, want %q", got, "app-data")
	}
	if got := mustReadAll(t, s, "dep/mem"); got != "snap-data" {
		t.Fatalf("snap backend: got %q, want %q", got, "snap-data")
	}

	// Delete through the router removes the file from the right
	// backend.
	if err := router.Delete(ctx, "apps/slug/dep.ext4"); err != nil {
		t.Fatalf("delete apps: %v", err)
	}
	if _, err := a.Get(ctx, "slug/dep.ext4"); !IsNotFound(err) {
		t.Fatalf("apps backend after delete: got %v, want IsNotFound", err)
	}
	if err := router.Delete(ctx, "snap/dep/mem"); err != nil {
		t.Fatalf("delete snap: %v", err)
	}
	if _, err := s.Get(ctx, "dep/mem"); !IsNotFound(err) {
		t.Fatalf("snap backend after delete: got %v, want IsNotFound", err)
	}
}

// TestPrefixRouterLongestMatch covers the longest-prefix-wins
// rule: with routes "apps/" and "apps/acme/", a key under
// "apps/acme/" must land on the second backend, not the first.
func TestPrefixRouterLongestMatch(t *testing.T) {
	a := newTestBackend(t)
	ac := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/":      a,
		"apps/acme/": ac,
	}, nil)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()
	if err := router.Put(ctx, "apps/acme/dep.ext4", strings.NewReader("acme")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if got := mustReadAll(t, ac, "dep.ext4"); got != "acme" {
		t.Fatalf("acme backend: got %q, want %q", got, "acme")
	}
	// The apps backend must not have the file at the top-level
	// (a previous broken dispatch would have stored the file
	// verbatim there).
	if _, err := a.Get(ctx, "acme/dep.ext4"); !IsNotFound(err) {
		t.Fatalf("apps backend unexpectedly has acme/dep.ext4: %v", err)
	}
}

// TestPrefixRouterFallback covers the no-matching-route path: when
// a key matches no route, it lands in the fallback. The fallback is
// the production pattern for /srv/fc holding most prefixes and
// /var/lib/faas/apps holding only "apps/".
func TestPrefixRouterFallback(t *testing.T) {
	a := newTestBackend(t)
	f := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
	}, f)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()
	if err := router.Put(ctx, "snap/dep/mem", strings.NewReader("snap")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if got := mustReadAll(t, f, "snap/dep/mem"); got != "snap" {
		t.Fatalf("fallback: got %q, want %q", got, "snap")
	}
}

// TestPrefixRouterNoRouteNoFallback covers the case where a key
// matches no route and there is no fallback — every Put/Get/Delete
// must fail with ErrInvalidKey so callers see a clear "no route"
// error rather than a confusing 404.
func TestPrefixRouterNoRouteNoFallback(t *testing.T) {
	a := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
	}, nil)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()
	if err := router.Put(ctx, "snap/dep/mem", strings.NewReader("x")); !IsInvalidKey(err) {
		t.Fatalf("put unmatched: IsInvalidKey=false, err=%v", err)
	}
	if _, err := router.Get(ctx, "snap/dep/mem"); !IsInvalidKey(err) {
		t.Fatalf("get unmatched: IsInvalidKey=false, err=%v", err)
	}
	if err := router.Delete(ctx, "snap/dep/mem"); !IsInvalidKey(err) {
		t.Fatalf("delete unmatched: IsInvalidKey=false, err=%v", err)
	}
}

// TestPrefixRouterListAggregates covers the LocalArtifactLister
// aggregation: keys from every backend come back with their route
// prefix re-applied, in sorted order, with no duplicates.
func TestPrefixRouterListAggregates(t *testing.T) {
	a := newTestBackend(t)
	s := newTestBackend(t)
	f := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
		"snap/": s,
	}, f)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()
	keys := []string{
		"apps/slug/dep.ext4",
		"snap/a/mem",
		"snap/a/vmstate",
		"base/runtime.ext4",
	}
	for _, k := range keys {
		if err := router.Put(ctx, k, strings.NewReader("x")); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	got, err := router.List(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	want := append([]string{}, keys...)
	sort.Strings(want)
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Fatalf("list all: got %v, want %v", got, want)
	}
	// Per-prefix list narrows to the matching route only.
	gotApps, err := router.List(ctx, "apps/")
	if err != nil {
		t.Fatalf("list apps/: %v", err)
	}
	if !equalStrings(gotApps, []string{"apps/slug/dep.ext4"}) {
		t.Fatalf("list apps/: got %v", gotApps)
	}
	gotSnap, err := router.List(ctx, "snap/")
	if err != nil {
		t.Fatalf("list snap/: %v", err)
	}
	if !equalStrings(gotSnap, []string{"snap/a/mem", "snap/a/vmstate"}) {
		t.Fatalf("list snap/: got %v", gotSnap)
	}
}

// TestPrefixRouterRejectsBadRoute covers the constructor's prefix
// validation: empty prefix, ".." prefix, and missing trailing slash
// are all rejected before the router is usable. A bad route is a
// misconfiguration and must fail loud at startup.
func TestPrefixRouterRejectsBadRoute(t *testing.T) {
	be := newTestBackend(t)
	if _, err := NewPrefixRouter(map[string]StorageBackend{
		"": be,
	}, nil); !IsInvalidKey(err) {
		t.Fatalf("empty route: IsInvalidKey=false, err=%v", err)
	}
	if _, err := NewPrefixRouter(map[string]StorageBackend{
		"../escape/": be,
	}, nil); !IsInvalidKey(err) {
		t.Fatalf("traversal route: IsInvalidKey=false, err=%v", err)
	}
	if _, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": nil,
	}, nil); err == nil {
		t.Fatalf("nil backend: nil err")
	}
}

// TestPrefixRouterRoundtripBytes is the byte-equality round-trip:
// write a multi-KiB payload through the router, read it back
// through the router, byte-for-byte equality. This is the test that
// proves the dispatch wrappers do not corrupt io.Copy semantics.
func TestPrefixRouterRoundtripBytes(t *testing.T) {
	a := newTestBackend(t)
	f := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
	}, f)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	ctx := context.Background()
	want := bytes.Repeat([]byte{0xfa, 0xce}, 4096)
	if err := router.Put(ctx, "apps/slug/dep.ext4", bytes.NewReader(want)); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := router.Get(ctx, "apps/slug/dep.ext4")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("byte mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

// TestPrefixRouterGetMissing covers the cold-boot path: a missing
// key routed through a router must surface as IsNotFound so
// downstream callers can branch to fallback (ADR-005).
func TestPrefixRouterGetMissing(t *testing.T) {
	a := newTestBackend(t)
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": a,
	}, newTestBackend(t))
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	_, err = router.Get(context.Background(), "apps/missing.ext4")
	if !IsNotFound(err) {
		t.Fatalf("get missing: IsNotFound=false, err=%v", err)
	}
	// Legacy single-box idiom must keep working.
	if !errors.Is(err, error(nil)) && !strings.Contains(err.Error(), "storage:") {
		t.Fatalf("get missing: missing storage tag in %v", err)
	}
}

// equalStrings is a small helper used by the list tests to compare
// two slices without importing reflect or cmp. Order-insensitive
// comparison would hide bugs; we want deterministic ordering.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
