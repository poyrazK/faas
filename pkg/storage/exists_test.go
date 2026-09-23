package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// noProbeBackend is a StorageBackend without the ExistenceChecker
// capability, standing in for GCS and test doubles.
type noProbeBackend struct{}

func (noProbeBackend) Put(context.Context, string, io.Reader) error { return nil }
func (noProbeBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (noProbeBackend) Delete(context.Context, string) error { return nil }

func TestLocalStorageBackend_Exists(t *testing.T) {
	root := t.TempDir()
	be, err := NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := be.Put(ctx, "apps/a/one.ext4", bytes.NewReader([]byte("layer"))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "apps", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps", "a", "empty.ext4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		want bool
	}{
		{"apps/a/one.ext4", true},
		{"apps/a/missing.ext4", false},
		{"apps/a/empty.ext4", false}, // Get reports a zero-byte artifact as not found
		{"apps/a", false},            // a directory is not an artifact
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got, err := be.Exists(ctx, tc.key)
			if err != nil {
				t.Fatalf("Exists: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Exists(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
	if _, err := be.Exists(ctx, "../escape"); !IsInvalidKey(err) {
		t.Fatalf("Exists(invalid) err = %v, want ErrInvalidKey", err)
	}
}

func TestOCIRegistryStorageBackend_ExistsNeverDownloadsBlob(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	const present = "apps/my-app/550e8400-e29b-41d4-a716-446655440000.ext4"
	const absent = "apps/my-app/660e8400-e29b-41d4-a716-446655440001.ext4"
	if err := be.Put(ctx, present, bytes.NewReader(bytes.Repeat([]byte{7}, 1<<16))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.mu.Lock()
	f.blobGets = 0
	f.mu.Unlock()

	for key, want := range map[string]bool{present: true, absent: false} {
		got, err := be.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists(%q): %v", key, err)
		}
		if got != want {
			t.Fatalf("Exists(%q) = %v, want %v", key, got, want)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blobGets != 0 {
		t.Fatalf("Exists downloaded %d blob(s); it must resolve the manifest only", f.blobGets)
	}
}

func TestExists_ThroughCacheAndRouter(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Put(ctx, "apps/a/one.ext4", bytes.NewReader([]byte("layer"))); err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(local, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	routedLocal, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{"apps/": routedLocal, "opaque/": noProbeBackend{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The router strips the matched prefix, so write through it.
	if err := router.Put(ctx, "apps/a/one.ext4", bytes.NewReader([]byte("layer"))); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name          string
		backend       StorageBackend
		key           string
		wantExists    bool
		wantSupported bool
	}{
		{"cache delegates hit", cache, "apps/a/one.ext4", true, true},
		{"cache delegates miss", cache, "apps/a/two.ext4", false, true},
		{"router routes hit", router, "apps/a/one.ext4", true, true},
		{"router routes miss", router, "apps/a/two.ext4", false, true},
		{"router to unprobeable backend", router, "opaque/x", false, false},
		{"plain backend unsupported", noProbeBackend{}, "apps/a/one.ext4", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exists, supported, err := Exists(ctx, tc.backend, tc.key)
			if err != nil {
				t.Fatalf("Exists: %v", err)
			}
			if exists != tc.wantExists || supported != tc.wantSupported {
				t.Fatalf("Exists = (%v, supported=%v), want (%v, %v)", exists, supported, tc.wantExists, tc.wantSupported)
			}
		})
	}

	// A cache over a parent that cannot probe must not answer from its own
	// copy: a cached artifact may already be gone from canonical storage.
	opaqueCache, err := NewLocalCacheBackend(noProbeBackend{}, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opaqueCache.Exists(ctx, "apps/a/one.ext4"); !errors.Is(err, ErrExistenceUnsupported) {
		t.Fatalf("cache over unprobeable parent err = %v, want ErrExistenceUnsupported", err)
	}
}
