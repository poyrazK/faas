package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Spools of drains this node no longer runs (deleted drains, apps and
// accounts, or drains whose app moved away) are removed once they have not
// been written for a day; the lease file, live drains and recent spools stay.
func TestAppLogDrainPruneSpoolsRemovesOrphans(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-appLogDrainSpoolOrphanTTL - time.Hour)
	makeSpool := func(name string, at time.Time) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"records.jsonl", "cursor.json", "dead-letters.jsonl"} {
			path := filepath.Join(dir, file)
			if err := os.WriteFile(path, []byte("{}\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	live := makeSpool("drain-live", old)
	recent := makeSpool("drain-recent", now.Add(-time.Hour))
	orphan := makeSpool("drain-deleted", old)
	stillWritten := makeSpool("drain-touched", old)
	if err := os.Chtimes(filepath.Join(stillWritten, "cursor.json"), now, now); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(root, ".manager.lock")
	if err := os.WriteFile(lock, nil, 0o640); err != nil {
		t.Fatal(err)
	}

	m := newAppLogDrainManager(nil, nil, nil, nil, nil)
	m.spoolRoot = root
	m.pruneSpoolsLocked(context.Background(), map[string]struct{}{"drain-live": {}}, now)

	for _, kept := range []string{live, recent, stillWritten, lock} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed: %v", filepath.Base(kept), err)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned spool still present: %v", err)
	}
}

func TestAppLogDrainSpoolBudgetBytes(t *testing.T) {
	for raw, want := range map[string]int64{"": 0, "junk": 0, "-5": 0, "1073741824": 1 << 30, " 4096 ": 4096} {
		if got := appLogDrainSpoolBudgetBytes(raw); got != want {
			t.Errorf("appLogDrainSpoolBudgetBytes(%q) = %d, want %d", raw, got, want)
		}
	}
}
