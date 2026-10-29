package storage_test

// adr: 20261026181418 — a generation-routed key keeps its logical name in the
// node cache and is read and written under its generation's key in shared
// storage, so a node on another release can never overwrite it.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	routedKey     = "base/base-amd64.ext4"
	generationKey = "base/generations/n/base-amd64.ext4"
)

type routedCacheFixture struct {
	shared storage.StorageBackend
	cache  *storage.LocalCacheBackend
}

func newRoutedCache(t *testing.T, route bool) routedCacheFixture {
	t.Helper()
	dir := t.TempDir()
	shared, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(remoteParent{shared}, filepath.Join(dir, "cache"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if route {
		if ok, err := cache.RouteGeneration(routedKey, generationKey); err != nil || !ok {
			t.Fatalf("RouteGeneration = %v, %v; want routed", ok, err)
		}
	}
	return routedCacheFixture{shared: shared, cache: cache}
}

func (f routedCacheFixture) put(t *testing.T, key, body string) {
	t.Helper()
	if err := f.shared.Put(t.Context(), key, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func (f routedCacheFixture) sharedObject(t *testing.T, key string) (string, bool) {
	t.Helper()
	rc, err := f.shared.Get(t.Context(), key)
	if errors.Is(err, storage.ErrNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return readCanonical(t, rc), true
}

func TestRoutedCacheReadsTheGenerationKey(t *testing.T) {
	cases := []struct {
		name string
		// shared objects before the read
		legacy, generation string
		read               func(context.Context, *storage.LocalCacheBackend) (io.ReadCloser, error)
		want               string
		wantErr            error
	}{
		{name: "Get misses into the generation key, not the logical one", legacy: "older release", generation: "this release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				return c.Get(ctx, routedKey)
			},
			want: "this release"},
		{name: "Refresh reads the generation key", legacy: "older release", generation: "this release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				return c.Refresh(ctx, routedKey)
			},
			want: "this release"},
		{name: "GetCanonical reads the generation key", legacy: "older release", generation: "this release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				return c.GetCanonical(ctx, routedKey)
			},
			want: "this release"},
		{name: "an unpublished generation is not served from the logical key", legacy: "older release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				return c.Get(ctx, routedKey)
			},
			wantErr: storage.ErrNotFound},
		{name: "GetParent reads the legacy logical key by its exact name", legacy: "older release", generation: "this release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				return c.GetParent(ctx, routedKey)
			},
			want: "older release"},
		{name: "RefreshFrom installs the named parent object under the logical key", legacy: "older release", generation: "this release",
			read: func(ctx context.Context, c *storage.LocalCacheBackend) (io.ReadCloser, error) {
				rc, err := c.RefreshFrom(ctx, routedKey, routedKey)
				if err != nil {
					return nil, err
				}
				_ = rc.Close()
				return c.Get(ctx, routedKey)
			},
			want: "older release"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRoutedCache(t, true)
			if tc.legacy != "" {
				f.put(t, routedKey, tc.legacy)
			}
			if tc.generation != "" {
				f.put(t, generationKey, tc.generation)
			}
			rc, err := tc.read(t.Context(), f.cache)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readCanonical(t, rc); got != tc.want {
				t.Fatalf("read %q, want %q", got, tc.want)
			}
		})
	}
}

// The 2026-10-26 mechanism, inverted: a routed node's writes never reach the
// shared logical key another release reads, and its deletes never remove it.
func TestRoutedCacheWritesNeverTouchTheLogicalKey(t *testing.T) {
	f := newRoutedCache(t, true)
	f.put(t, routedKey, "older release")
	if err := f.cache.Put(t.Context(), routedKey, strings.NewReader("this release")); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.sharedObject(t, routedKey); got != "older release" {
		t.Fatalf("shared logical key = %q, want the older release's copy untouched", got)
	}
	if got, ok := f.sharedObject(t, generationKey); !ok || got != "this release" {
		t.Fatalf("shared generation key = %q, %v; want this release's copy", got, ok)
	}
	cached, err := f.cache.Get(t.Context(), routedKey)
	if err != nil || readCanonical(t, cached) != "this release" {
		t.Fatalf("cached logical entry = %v; want this release's copy", err)
	}
	if err := f.cache.Delete(t.Context(), routedKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.sharedObject(t, generationKey); ok {
		t.Fatal("Delete left the generation key")
	}
	if got, _ := f.sharedObject(t, routedKey); got != "older release" {
		t.Fatalf("Delete reached the shared logical key: %q", got)
	}
}

func TestPublishCachedUploadsOnlyAMissingGenerationKey(t *testing.T) {
	cases := []struct {
		name       string
		published  string // generation key body before; "" = none
		wantUpload bool
		want       string
	}{
		{name: "a release before generation keys staged it", wantUpload: true, want: "verified bytes"},
		{name: "already published", published: "published bytes", want: "published bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRoutedCache(t, false)
			if err := f.cache.Put(t.Context(), routedKey, strings.NewReader("verified bytes")); err != nil {
				t.Fatal(err)
			}
			if ok, err := f.cache.RouteGeneration(routedKey, generationKey); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if tc.published != "" {
				f.put(t, generationKey, tc.published)
			}
			uploaded, err := f.cache.PublishCached(t.Context(), routedKey)
			if err != nil || uploaded != tc.wantUpload {
				t.Fatalf("PublishCached = %v, %v; want %v", uploaded, err, tc.wantUpload)
			}
			if got, _ := f.sharedObject(t, generationKey); got != tc.want {
				t.Fatalf("generation key = %q, want %q", got, tc.want)
			}
		})
	}
}

// A node-local parent is the copy the node boots, shared with no other node:
// nothing is routed there, through the cache or through a PrefixRouter.
func TestRouteGenerationLeavesNodeLocalKeysAlone(t *testing.T) {
	dir := t.TempDir()
	local, err := storage.NewLocalStorageBackend(filepath.Join(dir, "local"))
	if err != nil {
		t.Fatal(err)
	}
	remote, err := storage.NewLocalStorageBackend(filepath.Join(dir, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := storage.NewPrefixRouter(map[string]storage.StorageBackend{"base/": local}, remoteParent{remote})
	if err != nil {
		t.Fatal(err)
	}
	const scanKey, scanGeneration = "scans/base-amd64.ext4.scan.json", "scans/generations/n/base-amd64.ext4.scan.json"
	cases := []struct {
		name          string
		parent        storage.StorageBackend
		key, routedTo string
		want          bool
	}{
		{name: "single-box local parent", parent: local, key: routedKey, routedTo: generationKey},
		{name: "local base/ route", parent: router, key: routedKey, routedTo: generationKey},
		{name: "shared key routed to a local generation key", parent: router, key: scanKey, routedTo: generationKey},
		{name: "shared fallback route", parent: router, key: scanKey, routedTo: scanGeneration, want: true},
		{name: "shared parent", parent: remoteParent{remote}, key: routedKey, routedTo: generationKey, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := storage.NewLocalCacheBackend(tc.parent, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := storage.RouteGeneration(cache, tc.key, tc.routedTo)
			if err != nil || ok != tc.want {
				t.Fatalf("RouteGeneration = %v, %v; want %v", ok, err, tc.want)
			}
			if _, routed := cache.GenerationRoute(tc.key); routed != tc.want {
				t.Fatalf("GenerationRoute routed = %v, want %v", routed, tc.want)
			}
		})
	}
}
