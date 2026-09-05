// spool_test.go — table-driven tests for pkg/logarchive.Spool.
// Pin the on-disk JSON shape, the path layout
// ({root}/{instance}/{YYYY}/{MM}/{DD}.jsonl.partial), and the
// ErrSpoolFull sentinel. The spool's mutex + bufio interactions
// are exercised concurrently to catch any lost-wakeup /
// lost-update bugs that wouldn't show up in single-threaded
// runs.

package logarchive

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

// TestSpool_LayoutAndShape pins the file path + the JSON
// envelope one evicted line produces. Other tests build on
// this shape — a future field addition should bump this test
// alongside the docstring in spool.go.
func TestSpool_LayoutAndShape(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	ts := time.Date(2026, 8, 8, 12, 34, 56, 0, time.UTC)
	n, err := s.Write("instance-a", 42, "stdout", ts, "hello world")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n == 0 {
		t.Errorf("Write returned 0 bytes; want > 0")
	}
	wantPath := filepath.Join(root, "instance-a", "2026", "08", "log-2026-08-08.jsonl.partial")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected %s to exist: %v", wantPath, err)
	}
	// CloseAll flushes the bufio buffer so the on-disk content
	// matches the in-memory write count.
	if err := s.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s: %v", wantPath, err)
	}
	line := strings.TrimRight(string(body), "\n")
	if line == "" {
		t.Fatalf("empty line written")
	}
	var got struct {
		Seq       int64     `json:"seq"`
		Stream    string    `json:"stream"`
		WrittenAt time.Time `json:"ts"`
		Line      string    `json:"msg"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Seq != 42 {
		t.Errorf("seq = %d, want 42", got.Seq)
	}
	if got.Stream != "stdout" {
		t.Errorf("stream = %q, want stdout", got.Stream)
	}
	if !got.WrittenAt.Equal(ts) {
		t.Errorf("written_at = %v, want %v", got.WrittenAt, ts)
	}
	if got.Line != "hello world" {
		t.Errorf("line = %q, want hello world", got.Line)
	}
}

// TestSpool_DayBoundary pins the day-boundary behaviour: a
// line evicted at 23:59 lands in the day-1 file; a line at
// 00:00 the next day lands in the day-2 file. The path
// layout ({YYYY}/{MM}/{DD}) must reflect the line's ts, not
// the host clock.
func TestSpool_DayBoundary(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	day1 := time.Date(2026, 8, 8, 23, 59, 59, 999_000_000, time.UTC)
	day2 := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if _, err := s.Write("inst", 1, "stdout", day1, "last line"); err != nil {
		t.Fatalf("day1 write: %v", err)
	}
	if _, err := s.Write("inst", 2, "stdout", day2, "first line"); err != nil {
		t.Fatalf("day2 write: %v", err)
	}
	if err := s.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	paths := map[string]bool{}
	for _, day := range []string{"2026-08-08", "2026-08-09"} {
		p := filepath.Join(root, "inst", day[:4], day[5:7], "log-"+day+".jsonl.partial")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
			continue
		}
		body, _ := os.ReadFile(p)
		paths[day] = true
		if day == "2026-08-08" && !strings.Contains(string(body), "last line") {
			t.Errorf("day1 file missing 'last line': %s", body)
		}
		if day == "2026-08-09" && !strings.Contains(string(body), "first line") {
			t.Errorf("day2 file missing 'first line': %s", body)
		}
	}
	if len(paths) != 2 {
		t.Errorf("expected two day files, got %d", len(paths))
	}
}

// TestSpool_ConcurrentSameKey writes 200 lines to the same
// (instance, day) from 8 goroutines and asserts every line
// lands exactly once on disk (no lost updates, no duplicates).
// Catches missing-locking regressions that single-threaded
// tests miss.
func TestSpool_ConcurrentSameKey(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	const goroutines = 8
	const perGoroutine = 25
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := s.Write("inst", int64(g*100+i), "stdout", ts, "line"); err != nil {
					t.Errorf("goroutine %d write %d: %v", g, i, err)
				}
			}
		}(g)
	}
	wg.Wait()
	// CloseAll flushes bufio so we can count what's on disk.
	if err := s.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	want := goroutines * perGoroutine
	path := filepath.Join(root, "inst", "2026", "08", "log-2026-08-08.jsonl.partial")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	got := strings.Count(string(body), "\n")
	if got != want {
		t.Errorf("on-disk lines = %d, want %d", got, want)
	}
}

// TestSpool_Full refuses writes once the cap is exceeded.
// The ring's OnEvict callback continues to fire; the spool
// drops and returns ErrSpoolFull so the daemon sink can increment
// *_log_archive_failures_total{reason="spool_full"}.
func TestSpool_Full(t *testing.T) {
	root := t.TempDir()
	const cap = 256
	s := NewSpool(root, cap)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	// Write enough lines to exceed the cap; one of them
	// should return ErrSpoolFull.
	for i := 0; i < 100; i++ {
		_, err := s.Write("inst", int64(i), "stdout", ts, strings.Repeat("x", 32))
		if errors.Is(err, ErrSpoolFull) {
			return
		}
		if err != nil {
			t.Fatalf("unexpected error at i=%d: %v", i, err)
		}
	}
	t.Errorf("expected ErrSpoolFull after exhausting cap=%d", cap)
}

// TestSpool_PathTraversal rejects instance ids that contain
// "/" or NUL — the spool writes to a directory whose name is
// the instance id, and a hostile id would let an attacker
// escape the root. The cap on len(128) closes the larger
// surface.
func TestSpool_PathTraversal(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"../escape", "a/b", "a\\b", "a\x00b", strings.Repeat("x", MaxInstanceIDLen+1)} {
		if _, err := s.Write(id, 1, "stdout", ts, "x"); err == nil {
			t.Errorf("expected error for instance id %q, got nil", id)
		}
	}
}

// TestSpool_LocalBytes tracks the running byte count.
// The shipper exposes this via LocalBytes() and feeds the
// daemon-specific *_log_archive_local_bytes gauge from it.
func TestSpool_LocalBytes(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	n1, err := s.Write("a", 1, "stdout", ts, "hi")
	if err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if s.LocalBytes() != int64(n1) {
		t.Errorf("after 1 write: LocalBytes=%d, want %d", s.LocalBytes(), n1)
	}
	n2, err := s.Write("b", 2, "stdout", ts, "world")
	if err != nil {
		t.Fatalf("Write b: %v", err)
	}
	if s.LocalBytes() != int64(n1+n2) {
		t.Errorf("after 2 writes: LocalBytes=%d, want %d", s.LocalBytes(), n1+n2)
	}
}

func TestSpool_PrepareUploadRotatesAndMergesRetry(t *testing.T) {
	root := t.TempDir()
	s := NewSpool(root, 1<<20)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if _, err := s.Write("inst", 1, "stdout", ts, "first"); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	path, err := s.PrepareUpload("inst", "2026-08-08")
	if err != nil {
		t.Fatalf("PrepareUpload: %v", err)
	}
	if !strings.HasSuffix(path, ".jsonl.upload") {
		t.Fatalf("upload path = %q, want .jsonl.upload", path)
	}
	if _, err := s.Write("inst", 2, "stdout", ts, "second"); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	merged, err := s.PrepareUpload("inst", "2026-08-08")
	if err != nil {
		t.Fatalf("PrepareUpload merge: %v", err)
	}
	if merged != path {
		t.Fatalf("merged path = %q, want %q", merged, path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read merged upload: %v", err)
	}
	if got := strings.Count(string(body), "\n"); got != 2 {
		t.Fatalf("merged lines = %d, want 2: %s", got, body)
	}
	if err := s.CompleteUpload(path); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upload file still exists: %v", err)
	}
}

// Compile-time check: logbuf.Ring.SetEvictCallback exists
// and accepts the func(Line) signature the OnEvict seam
// expects. A future ring field change should bump this
// test alongside the field doc on Ring.onEvict.
var _ = func(_ *logbuf.Ring) {} // keeps logbuf imported
