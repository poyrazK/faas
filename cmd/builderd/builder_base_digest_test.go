package main

// Tests for resolveBuilderBaseDigestPath (issue #2577).
//
// The base path and its digest sidecar must be resolved through the SAME
// backend. Deriving the sidecar as base + ".digest" only works when the base
// is a plain file in the storage root; when resolveBuilderBasePath returns a
// read-through cache path the base is content-addressed and no sibling
// sidecar exists, so every build failed with "stat builder base digest
// sidecar: no such file or directory".

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

// nonResolvingBackend is a StorageBackend that is NOT a LocalPathResolver.
type nonResolvingBackend struct {
	storage.StorageBackend
}

func TestResolveBuilderBaseDigestPath_ResolvesThroughStorage(t *testing.T) {
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	key := sched.BaseDigestKey("builder")
	path := filepath.Join(root, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("sha256:deadbeef\nfaas-base-layout-v3\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, err := resolveBuilderBaseDigestPath(be)
	if err != nil {
		t.Fatalf("resolveBuilderBaseDigestPath: %v", err)
	}
	want, local, err := be.LocalPath(key)
	if err != nil || !local {
		t.Fatalf("LocalPath(%s) = %q, %v, %v", key, want, local, err)
	}
	if got != want {
		t.Fatalf("resolveBuilderBaseDigestPath = %q, want %q", got, want)
	}
}

// It must resolve the DIGEST key, not the base key. Returning the base blob
// would produce a confusing "invalid identity" failure instead of a clear one.
func TestResolveBuilderBaseDigestPath_UsesTheDigestKeyNotTheBaseKey(t *testing.T) {
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{sched.BaseKey("builder"), sched.BaseDigestKey("builder")} {
		path := filepath.Join(root, key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(key), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	got, err := resolveBuilderBaseDigestPath(be)
	if err != nil {
		t.Fatalf("resolveBuilderBaseDigestPath: %v", err)
	}
	basePath, _, _ := be.LocalPath(sched.BaseKey("builder"))
	if got == basePath {
		t.Fatalf("resolveBuilderBaseDigestPath returned the base path %q, not the sidecar", got)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sched.BaseDigestKey("builder") {
		t.Errorf("resolved path holds %q, want the digest-key blob", string(data))
	}
}

// An empty result is the signal to keep the sibling derivation, which is what
// a cold cache and a non-resolving backend both need.
func TestResolveBuilderBaseDigestPath_EmptyWhenNotLocallyResolvable(t *testing.T) {
	tests := []struct {
		name    string
		backend func(t *testing.T) storage.StorageBackend
	}{
		{
			name:    "nil backend",
			backend: func(*testing.T) storage.StorageBackend { return nil },
		},
		{
			name: "backend is not a LocalPathResolver",
			backend: func(t *testing.T) storage.StorageBackend {
				be, err := storage.NewLocalStorageBackend(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				return nonResolvingBackend{StorageBackend: be}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBuilderBaseDigestPath(tc.backend(t))
			if err != nil {
				t.Fatalf("resolveBuilderBaseDigestPath: %v", err)
			}
			if got != "" {
				t.Errorf("resolveBuilderBaseDigestPath = %q, want \"\" so the "+
					"sibling derivation is kept", got)
			}
		})
	}
}

// LocalPath resolves a key to a path without requiring the file to exist, so
// on the local backend an absent sidecar still yields the canonical path —
// which is precisely the sibling of the base. Behaviour there is therefore
// unchanged by this fix, and the missing file still surfaces as a clear
// "stat builder base digest sidecar" error rather than something subtler.
func TestResolveBuilderBaseDigestPath_LocalBackendAbsentSidecarMatchesTheSibling(t *testing.T) {
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveBuilderBaseDigestPath(be)
	if err != nil {
		t.Fatalf("resolveBuilderBaseDigestPath: %v", err)
	}
	basePath, local, err := be.LocalPath(sched.BaseKey("builder"))
	if err != nil || !local {
		t.Fatalf("LocalPath(base) = %q, %v, %v", basePath, local, err)
	}
	if want := basePath + ".digest"; got != want {
		t.Errorf("resolveBuilderBaseDigestPath = %q, want %q (the sibling); on a "+
			"local backend the storage-resolved and derived paths must agree", got, want)
	}
	if _, statErr := os.Stat(got); statErr == nil {
		t.Fatal("fixture unexpectedly created the sidecar; this test covers the absent case")
	}
}
