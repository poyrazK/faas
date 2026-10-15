package logarchive

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// H10-80: purged markers left their per-instance directories behind, and the
// startup walk over tens of thousands of them outran systemd's start timeout.
// PruneEmptyDirs removes empty trees in one pass and keeps anything with files.
func TestSpoolPruneEmptyDirsKeepsFilesAndRoot(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "inst-gone", "2026", "10")
	if err := os.MkdirAll(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(root, "inst-live", "2026", "10")
	if err := os.MkdirAll(live, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(live, "log-2026-10-14.jsonl.gz")
	if err := os.WriteFile(marker, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	s := NewSpool(root, 1<<20)
	if got := s.PruneEmptyDirs(); got != 3 {
		t.Fatalf("removed %d directories, want 3 (inst-gone, 2026, 10)", got)
	}
	if _, err := os.Stat(filepath.Join(root, "inst-gone")); !os.IsNotExist(err) {
		t.Fatalf("empty instance tree still present: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker with content was removed: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("spool root removed: %v", err)
	}
	// A write after pruning recreates its directory.
	if _, err := s.Write("inst-gone", 1, "stdout", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "hello"); err != nil {
		t.Fatalf("write after prune: %v", err)
	}
}

// PurgeOnce prunes the directories its own file purge emptied.
func TestShipperPurgeOncePrunesEmptiedDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst-old", "2026", "09")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "log-2026-09-01.jsonl.gz")
	if err := os.WriteFile(old, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	sh := &Shipper{cfg: Config{Bucket: "b", SpoolRoot: root, RetentionDays: 7}, spool: NewSpool(root, 1<<20), log: slog.New(slog.NewTextHandler(io.Discard, nil)), now: time.Now}
	n, err := sh.PurgeOnce(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("PurgeOnce = (%d, %v), want (1, nil)", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, "inst-old")); !os.IsNotExist(err) {
		t.Fatalf("emptied instance tree still present: %v", err)
	}
}
