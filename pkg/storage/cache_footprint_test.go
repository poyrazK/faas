// adr: 633
package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeExtents replaces the platform extent reader for one test.
func fakeExtents(t *testing.T, byPath func(path string) ([]physicalExtent, error)) {
	t.Helper()
	prev := readExtents
	readExtents = byPath
	t.Cleanup(func() { readExtents = prev })
}

func TestCacheFootprintCountsSharedBlocksOnce(t *testing.T) {
	const mib = int64(1) << 20
	extents := map[string][]physicalExtent{
		// The app layer and a snapshot drive cloned from it share 300 MiB;
		// the guest wrote 5 MiB of its own.
		"layer": {{physical: 0, length: 300 * mib, shared: true}},
		"drive": {{physical: 0, length: 200 * mib, shared: true}, {physical: 200 * mib, length: 100 * mib, shared: true}, {physical: 900 * mib, length: 5 * mib}},
		"mem":   {{physical: 1000 * mib, length: 240 * mib}},
		// Extent placement unknown: fall back to the allocated size.
		"opaque": nil,
	}
	fakeExtents(t, func(path string) ([]physicalExtent, error) {
		if path == "opaque" {
			return nil, errExtentsUnsupported
		}
		return extents[path], nil
	})
	f := newCacheFootprint([]cacheEntry{
		{path: "layer", size: 300 * mib},
		{path: "drive", size: 305 * mib},
		{path: "mem", size: 240 * mib},
		{path: "opaque", size: 7 * mib},
	})
	if got, want := f.total(), (300+5+240+7)*mib; got != want {
		t.Fatalf("total = %d MiB, want %d MiB", got/mib, want/mib)
	}
	f.drop(1) // evicting the drive frees only what the guest wrote
	if got, want := f.total(), (300+240+7)*mib; got != want {
		t.Fatalf("after evicting the drive = %d MiB, want %d MiB", got/mib, want/mib)
	}
	f.drop(0)
	if got, want := f.total(), (240+7)*mib; got != want {
		t.Fatalf("after evicting the layer = %d MiB, want %d MiB", got/mib, want/mib)
	}
}

// TestEnforceBudgetDoesNotEvictForSharedBlocks pins the economics of ADR-633:
// a cache whose files share blocks is judged by its physical footprint, so a
// drive that is a clone of a cached layer does not evict another snapshot.
func TestEnforceBudgetDoesNotEvictForSharedBlocks(t *testing.T) {
	const block = 64 << 10
	cases := []struct {
		name      string
		maxBytes  int64
		wantEvict []string
	}{
		// Per-file allocation is three blocks, over this budget; the
		// physical footprint is two.
		{"shared blocks fit the budget", 3*block - 1, nil},
		// Evicting the layer frees nothing while the drive still holds its
		// blocks, so eviction continues to the drive.
		{"over budget even counted once", 2*block - 1, []string{"apps/layer.ext4", "snap/dep/captures/c/v2/drive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := NewLocalCacheBackend(newBudgetParent(), t.TempDir(), 1<<30)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{"apps/layer.ext4", "snap/dep/captures/c/v2/drive", "snap/dep/captures/c/v2/mem"}
			body := bytes.Repeat([]byte{7}, block)
			past := time.Now().Add(-time.Hour)
			paths := map[string]string{}
			for i, key := range keys {
				if err := cache.Put(context.Background(), key, bytes.NewReader(body)); err != nil {
					t.Fatal(err)
				}
				path, ok, err := cache.LocalPath(key)
				if err != nil || !ok {
					t.Fatalf("%s not cached: %v", key, err)
				}
				paths[path] = key
				stamp := past.Add(time.Duration(i) * time.Minute)
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			// Layer and drive share one physical block range; mem is its own.
			fakeExtents(t, func(path string) ([]physicalExtent, error) {
				switch {
				case strings.HasSuffix(paths[path], "/mem"):
					return []physicalExtent{{physical: 10 * block, length: block}}, nil
				case paths[path] != "":
					return []physicalExtent{{physical: 0, length: block, shared: true}}, nil
				}
				return nil, errExtentsUnsupported
			})
			cache.maxBytes = tc.maxBytes
			cache.mu.Lock()
			err = cache.enforceBudgetLocked()
			cache.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			var evicted []string
			for path, key := range paths {
				if _, err := os.Stat(path); os.IsNotExist(err) {
					evicted = append(evicted, key)
				}
			}
			sort.Strings(evicted)
			if strings.Join(evicted, ",") != strings.Join(tc.wantEvict, ",") {
				t.Fatalf("evicted %v, want %v", evicted, tc.wantEvict)
			}
		})
	}
}

func TestCloneSpoolOnlyClonesAnUnreadSnapshotFile(t *testing.T) {
	dir := t.TempDir()
	src, err := os.Create(filepath.Join(dir, "drive"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	if _, err := src.WriteString("drive-bytes"); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(dir, "spool")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Close() }()
	if _, ok := cloneSpool(tmp, bytes.NewReader(nil), "snap/d/captures/c/v2/drive"); ok {
		t.Fatal("cloned a reader that is not a file")
	}
	if _, ok := cloneSpool(tmp, src, "sigs/x"); ok {
		t.Fatal("cloned a key outside the sparse snapshot artifacts")
	}
	// src's offset is at its end after the write: a clone would publish
	// bytes the caller did not hand over.
	if _, ok := cloneSpool(tmp, src, "snap/d/captures/c/v2/drive"); ok {
		t.Fatal("cloned a partly read file")
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if n, ok := cloneSpool(tmp, src, "snap/d/captures/c/v2/drive"); ok && n != int64(len("drive-bytes")) {
		t.Fatalf("cloned length = %d", n)
	}
}

// budgetParent is a parent that accepts every Put.
type budgetParent struct{ blobs map[string][]byte }

func newBudgetParent() *budgetParent { return &budgetParent{blobs: map[string][]byte{}} }

func (p *budgetParent) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	p.blobs[key] = b
	return err
}

func (p *budgetParent) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := p.blobs[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (p *budgetParent) Delete(_ context.Context, key string) error {
	delete(p.blobs, key)
	return nil
}
