package fcvm

// adr: 20261110210000

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeFill(data string, calls *atomic.Int32) func(context.Context, *os.File) error {
	return func(_ context.Context, f *os.File) error {
		calls.Add(1)
		_, err := f.WriteString(data)
		return err
	}
}

// One decrypt per capture: concurrent wakes share the fill, later wakes hit
// the entry, and the directory stays root-only.
func TestSealedPlainCacheFillsOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	c, err := NewSealedPlainCache(dir, 1<<20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	var calls atomic.Int32
	var wg sync.WaitGroup
	paths := make([]string, 8)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := c.Ensure(context.Background(), "capture-a", "snap/mem", writeFill("plain-a", &calls))
			if err != nil {
				t.Error(err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fills = %d, want 1", calls.Load())
	}
	for _, p := range paths {
		if got, _ := os.ReadFile(p); string(got) != "plain-a" {
			t.Fatalf("entry %s = %q", p, got)
		}
	}
	// Another capture under the same storage key is its own entry.
	p, err := c.Ensure(context.Background(), "capture-b", "snap/mem", writeFill("plain-b", &calls))
	if err != nil || p == paths[0] {
		t.Fatalf("re-capture shares the entry: %s, %v", p, err)
	}
	if got, _ := os.ReadFile(p); string(got) != "plain-b" {
		t.Fatalf("re-capture entry = %q", got)
	}
	// A linked staging file survives the entry's eviction.
	staged := filepath.Join(t.TempDir(), "staged")
	if err := (sealedPlainLinker{path: paths[0]}).LinkTo(staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(staged); string(got) != "plain-a" {
		t.Fatalf("staged after eviction = %q", got)
	}
}

// A failed decrypt leaves no entry and no partial file, and the next wake
// tries again.
func TestSealedPlainCacheFailedFill(t *testing.T) {
	dir := t.TempDir()
	c, err := NewSealedPlainCache(dir, 1<<20, 0)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("tampered")
	if _, err := c.Ensure(context.Background(), "cap", "k", func(_ context.Context, f *os.File) error {
		_, _ = f.WriteString("partial")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %d files after a failed fill", len(entries))
	}
	var calls atomic.Int32
	if _, err := c.Ensure(context.Background(), "cap", "k", writeFill("ok", &calls)); err != nil || calls.Load() != 1 {
		t.Fatalf("retry: calls=%d err=%v", calls.Load(), err)
	}
}

// Entries unused past maxAge go, then the oldest until the budget holds;
// the entry just filled stays.
func TestSealedPlainCacheEvicts(t *testing.T) {
	dir := t.TempDir()
	c, err := NewSealedPlainCache(dir, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	var calls atomic.Int32
	old, _ := c.Ensure(context.Background(), "cap", "old", writeFill("old", &calls))
	_ = os.Chtimes(old, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	keep, err := c.Ensure(context.Background(), "cap", "new", writeFill("new", &calls))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired entry kept: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("fresh entry evicted: %v", err)
	}
}

func TestNewSealedPlainCacheRefusesBadConfig(t *testing.T) {
	if _, err := NewSealedPlainCache("relative", 1, 0); err == nil {
		t.Fatal("relative dir accepted")
	}
	if _, err := NewSealedPlainCache(t.TempDir(), 0, 0); err == nil {
		t.Fatal("zero budget accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sealedPlainTmpPrefix+"x"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSealedPlainCache(dir, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, sealedPlainTmpPrefix+"x")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a crashed fill's partial file survived the restart")
	}
}
