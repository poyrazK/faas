package storage_test

// adr: 20261024191249 — a pre-stage and the activation gate read the shared
// runtime-base publication without changing what this node's daemons read.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// remoteParent hides the local backend's LocalPath, as a registry parent
// does, so LocalPath through the cache reports only cache entries.
type remoteParent struct{ b storage.StorageBackend }

func (p remoteParent) Put(ctx context.Context, key string, r io.Reader) error {
	return p.b.Put(ctx, key, r)
}
func (p remoteParent) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return p.b.Get(ctx, key)
}
func (p remoteParent) Delete(ctx context.Context, key string) error { return p.b.Delete(ctx, key) }

func readCanonical(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestGetCanonicalReadsTheParentAndLeavesTheCacheAlone(t *testing.T) {
	cases := []struct {
		name      string
		routed    bool   // reach the cache through a PrefixRouter
		cached    string // "" = nothing cached
		published string // "" = nothing published
		wantBody  string
		wantErr   error
	}{
		{name: "parent newer than the cached copy", cached: "old", published: "new", wantBody: "new"},
		{name: "nothing cached", published: "new", wantBody: "new"},
		{name: "nothing published", cached: "old", wantErr: storage.ErrNotFound},
		{name: "routed: parent newer than the cached copy", routed: true, cached: "old", published: "new", wantBody: "new"},
		{name: "routed: nothing published", routed: true, wantErr: storage.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			local, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
			if err != nil {
				t.Fatal(err)
			}
			parent := remoteParent{local}
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			// The cache and its parent see the key the route leaves.
			key, rel := "base-amd64.scan.json", "base-amd64.scan.json"
			var root storage.StorageBackend = cache
			if tc.routed {
				key = "scans/" + rel
				if root, err = storage.NewPrefixRouter(map[string]storage.StorageBackend{"scans/": cache}, nil); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cached != "" {
				if err := cache.Put(t.Context(), rel, strings.NewReader(tc.cached)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.published != "" {
				if err := parent.Put(t.Context(), rel, strings.NewReader(tc.published)); err != nil {
					t.Fatal(err)
				}
			} else if err := parent.Delete(t.Context(), rel); err != nil && !errors.Is(err, storage.ErrNotFound) {
				t.Fatal(err)
			}

			rc, err := storage.GetCanonical(t.Context(), root, key)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("GetCanonical err = %v, want %v", err, tc.wantErr)
				}
			case err != nil:
				t.Fatal(err)
			default:
				if body := readCanonical(t, rc); body != tc.wantBody {
					t.Fatalf("GetCanonical = %q, want %q", body, tc.wantBody)
				}
			}

			path, cached, err := cache.LocalPath(rel)
			if err != nil {
				t.Fatal(err)
			}
			if cached != (tc.cached != "") {
				t.Fatalf("cache entry present = %v after GetCanonical, want %v", cached, tc.cached != "")
			}
			if cached {
				if body, err := readCachedFile(path); err != nil || body != tc.cached {
					t.Fatalf("cached copy = %q (%v) after GetCanonical, want %q", body, err, tc.cached)
				}
			}
		})
	}
}

func readCachedFile(path string) (string, error) {
	local, err := storage.NewLocalStorageBackend(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	rc, err := local.Get(context.Background(), filepath.Base(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	return string(body), err
}

// An uncached route has no other copy: its ordinary Get is canonical.
func TestGetCanonicalOnAnUncachedRoute(t *testing.T) {
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Put(t.Context(), "base/base-amd64.digest", strings.NewReader("sha256:1")); err != nil {
		t.Fatal(err)
	}
	rc, err := storage.GetCanonical(t.Context(), local, "base/base-amd64.digest")
	if err != nil {
		t.Fatal(err)
	}
	if body := readCanonical(t, rc); body != "sha256:1" {
		t.Fatalf("GetCanonical = %q", body)
	}
	if _, err := storage.GetCanonical(t.Context(), nil, "base/x"); err == nil {
		t.Fatal("GetCanonical without a backend succeeded")
	}
}
