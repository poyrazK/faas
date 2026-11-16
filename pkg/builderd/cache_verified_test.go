package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// countHashes makes c count the artifact reads its lookups make.
func countHashes(c *Cache) *atomic.Int32 {
	var n atomic.Int32
	c.hashArtifact = func(path string) (string, os.FileInfo, error) {
		n.Add(1)
		return hashFileInfo(path)
	}
	return &n
}

func storeTestBuild(t *testing.T, c *Cache, recipe BuildCacheRecipe, data string) {
	t.Helper()
	layer := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layer, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.StoreBuild(recipe, layer, int64(len(data))); err != nil {
		t.Fatal(err)
	}
}

func TestCacheHitReusesTheDigestItComputed(t *testing.T) {
	const data = "artifact bytes"
	sum := sha256.Sum256([]byte(data))
	want := hex.EncodeToString(sum[:])
	tests := []struct {
		name string
		// restart looks the entry up from a second Cache, as builderd does
		// after a restart, which hashes the artifact once.
		restart    bool
		wantHashes int32
	}{
		{name: "stored by this process", wantHashes: 0},
		{name: "after a restart", restart: true, wantHashes: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			c := NewCache(root)
			recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
			storeTestBuild(t, c, recipe, data)
			if tt.restart {
				c = NewCache(root)
			}
			hashes := countHashes(c)
			if got := c.ClassifyBuild(recipe); got != cacheOutcomeHit {
				t.Fatalf("ClassifyBuild = %q, want hit", got)
			}
			for i := range 2 {
				entry, ok, err := c.LeaseBuild(recipe, fmt.Sprintf("dep-%d", i))
				if err != nil || !ok {
					t.Fatalf("LeaseBuild %d = %v, %v", i, ok, err)
				}
				if entry.Digest != want {
					t.Fatalf("lease digest = %q, want %q", entry.Digest, want)
				}
			}
			if got := hashes.Load(); got != tt.wantHashes {
				t.Fatalf("artifact hashed %d times, want %d", got, tt.wantHashes)
			}
		})
	}
}

func TestCacheRememberedDigestFollowsTheFile(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{name: "rewritten in place, same size", change: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("other bytes!!!"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "replaced, same size and time", change: func(t *testing.T, path string) {
			old, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			next := path + ".next"
			if err := os.WriteFile(next, []byte("other bytes!!!"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(next, old.ModTime(), old.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(next, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "replaced by a symlink to itself", change: func(t *testing.T, path string) {
			kept := path + ".kept"
			if err := os.Rename(path, kept); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(kept, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			c := NewCache(root)
			recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
			storeTestBuild(t, c, recipe, "artifact bytes")
			entry, ok := c.LookupBuild(recipe)
			if !ok {
				t.Fatal("initial miss")
			}
			tt.change(t, entry.Path)
			if _, ok := c.LookupBuild(recipe); ok {
				t.Fatal("changed artifact still a hit")
			}
			if got := c.ClassifyBuild(recipe); got != cacheOutcomeInvalidated {
				t.Fatalf("ClassifyBuild = %q, want invalidated", got)
			}
		})
	}
}

func TestVerifiedArtifactsTrustOnlySettledFilesAndStayBounded(t *testing.T) {
	dir := t.TempDir()
	settledFile := func(name string) (string, os.FileInfo) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		settled := time.Now().Add(-time.Minute)
		if err := os.Chtimes(path, settled, settled); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return path, info
	}

	t.Run("a just-written file is not remembered", func(t *testing.T) {
		path := filepath.Join(dir, "fresh")
		if err := os.WriteFile(path, []byte("fresh"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		var m verifiedArtifacts
		m.record(path, "digest", info, time.Now())
		if _, ok := m.digest(path, info); ok {
			t.Fatal("remembered a digest for a file modified just now")
		}
	})

	t.Run("the oldest record leaves at the bound", func(t *testing.T) {
		var m verifiedArtifacts
		start := time.Now()
		firstPath, firstInfo := settledFile("record-0")
		m.record(firstPath, "digest-0", firstInfo, start)
		for i := 1; i < api.BuilderdVerifiedCacheArtifacts; i++ {
			path := filepath.Join(dir, fmt.Sprintf("record-%d", i))
			m.record(path, "digest", firstInfo, start.Add(time.Duration(i)*time.Millisecond))
		}
		if _, ok := m.digest(firstPath, firstInfo); !ok {
			t.Fatal("first record gone before the bound")
		}
		lastPath, lastInfo := settledFile("record-last")
		m.record(lastPath, "digest-last", lastInfo, start.Add(time.Hour))
		if _, ok := m.digest(firstPath, firstInfo); ok {
			t.Fatal("oldest record kept past the bound")
		}
		if got, ok := m.digest(lastPath, lastInfo); !ok || got != "digest-last" {
			t.Fatalf("newest record = %q, %v", got, ok)
		}
		if len(m.entries) > api.BuilderdVerifiedCacheArtifacts {
			t.Fatalf("%d records, bound %d", len(m.entries), api.BuilderdVerifiedCacheArtifacts)
		}
	})
}
