package logarchive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSpoolFile(t *testing.T, root, instance, name string, size int) string {
	t.Helper()
	dir := filepath.Join(root, instance, "2026", "10")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

// H10-80: NewSpool walked the whole spool, which took 130 s with a cold
// dentry cache and held vmmd's startup. It must not read the root; the
// shipper counts leftover unshipped bytes later, once.
func TestSpoolCountExistingRunsOutsideNewSpool(t *testing.T) {
	root := t.TempDir()
	writeSpoolFile(t, root, "inst-a", "log-2026-10-14.jsonl.partial", 10)
	writeSpoolFile(t, root, "inst-b", "log-2026-10-14.jsonl.upload", 5)
	writeSpoolFile(t, root, "inst-c", "log-2026-10-13.jsonl.gz", 100)

	s := NewSpool(root, 1<<20)
	if got := s.LocalBytes(); got != 0 {
		t.Fatalf("LocalBytes after NewSpool = %d, want 0 (no startup walk)", got)
	}
	s.CountExisting()
	if got := s.LocalBytes(); got != 15 {
		t.Fatalf("LocalBytes after CountExisting = %d, want 15 (.partial + .upload, not .gz)", got)
	}
	s.CountExisting()
	if got := s.LocalBytes(); got != 15 {
		t.Fatalf("second CountExisting changed LocalBytes to %d, want 15", got)
	}
}

// A file reopened by a write before the count is counted at open, and the
// later walk must not add its bytes again.
func TestSpoolCountExistingSkipsFilesReopenedBeforeCount(t *testing.T) {
	root := t.TempDir()
	writeSpoolFile(t, root, "inst-a", "log-2026-10-15.jsonl.partial", 40)
	writeSpoolFile(t, root, "inst-b", "log-2026-10-15.jsonl.partial", 7)

	s := NewSpool(root, 1<<20)
	n, err := s.Write("inst-a", 1, "stdout", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC), "hello")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.FlushKey("inst-a", "2026-10-15"); err != nil {
		t.Fatalf("FlushKey: %v", err)
	}
	if got, want := s.LocalBytes(), int64(40+n); got != want {
		t.Fatalf("LocalBytes after reopen = %d, want %d", got, want)
	}
	s.CountExisting()
	if got, want := s.LocalBytes(), int64(40+n+7); got != want {
		t.Fatalf("LocalBytes after CountExisting = %d, want %d", got, want)
	}
	// After the count, opening another leftover file adds nothing: the
	// walk already counted it.
	n2, err := s.Write("inst-b", 1, "stdout", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC), "x")
	if err != nil {
		t.Fatalf("Write inst-b: %v", err)
	}
	if got, want := s.LocalBytes(), int64(40+n+7+n2); got != want {
		t.Fatalf("LocalBytes after post-count reopen = %d, want %d", got, want)
	}
}

// RunOnce counts before its first snapshot, so a restarted shipper that
// drains leftover work ends at zero rather than below it.
func TestShipperRunOnceCountsLeftoverBytes(t *testing.T) {
	sh, _, _ := newTestShipper(t, 7)
	path := writeSpoolFile(t, sh.cfg.SpoolRoot, "inst-a", "log-2026-10-14.jsonl.partial", 0)
	if err := os.WriteFile(path, []byte(`{"seq":1,"stream":"stdout","ts":"2026-10-14T09:00:00Z","msg":"left"}`+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got := sh.spool.LocalBytes(); got != 0 {
		t.Fatalf("LocalBytes before RunOnce = %d, want 0", got)
	}
	if _, _, err := sh.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !sh.spool.counted {
		t.Fatal("RunOnce did not count leftover bytes")
	}
	if got := sh.spool.LocalBytes(); got != 0 {
		t.Fatalf("LocalBytes after shipping leftovers = %d, want 0", got)
	}
}
