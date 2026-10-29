package sourcespool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeStore struct {
	paths []string
	err   error
}

func (f fakeStore) ListInFlightDeploymentSpoolPaths(context.Context) ([]string, error) {
	return f.paths, f.err
}

var (
	sweepNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	stale    = sweepNow.Add(-25 * time.Hour)
	fresh    = sweepNow.Add(-time.Hour)
)

func writeAged(t *testing.T, root, rel string, at time.Time) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

func ageDirs(t *testing.T, root string, at time.Time) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			_ = os.Chtimes(p, at, at)
		}
		return nil
	})
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestSweepBuildSpool(t *testing.T) {
	root := t.TempDir()
	const logID = "0f5c8a52-7a1e-4c1e-9f3a-2d5b6c7d8e9f"
	const liveLogID = "1f5c8a52-7a1e-4c1e-9f3a-2d5b6c7d8e9f"
	const freshLogID = "2f5c8a52-7a1e-4c1e-9f3a-2d5b6c7d8e9f"
	removed := []string{
		writeAged(t, root, "0123456789abcdef01234567.tar.gz", stale),
		writeAged(t, root, "0123456789abcdef01234568.tar.gz.tmp", stale),
		writeAged(t, root, "dev-source-42.tar.gz", stale),
		writeAged(t, root, ".source-download-9", stale),
		writeAged(t, root, "projects/acct/proj/app.tar.gz", stale),
		writeAged(t, root, logID+"/build.log", stale),
	}
	inFlight := writeAged(t, root, "abcdefabcdefabcdefabcdef.tar.gz", stale)
	liveLog := writeAged(t, root, liveLogID+"/build.log", stale)
	kept := []string{
		inFlight, liveLog,
		writeAged(t, root, "fedcbafedcbafedcbafedcba.tar.gz", fresh),
		writeAged(t, root, "fedcbafedcbafedcbafedcbb.part", stale),
		writeAged(t, root, "git-context-1.tar.gz", stale),
		writeAged(t, root, "dev-source-cache/abc", stale),
		writeAged(t, root, "notes.tar.gz", stale),
		writeAged(t, root, freshLogID+"/build.log", fresh),
	}
	ageDirs(t, root, stale)
	sw := Sweeper{Root: root, Layout: BuildSpool, MaxAge: 24 * time.Hour,
		Now: func() time.Time { return sweepNow }, Store: fakeStore{paths: []string{inFlight, liveLog}}}
	n, err := sw.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if n != len(removed) {
		t.Errorf("removed %d files, want %d", n, len(removed))
	}
	for _, p := range removed {
		if exists(p) {
			t.Errorf("%s survived the sweep", p)
		}
	}
	for _, p := range kept {
		if !exists(p) {
			t.Errorf("%s was removed", p)
		}
	}
	if exists(filepath.Join(root, logID)) {
		t.Error("expired log directory was left behind")
	}
}

func TestSweepStoreErrorRemovesNothing(t *testing.T) {
	root := t.TempDir()
	p := writeAged(t, root, "0123456789abcdef01234567.tar.gz", stale)
	sw := Sweeper{Root: root, MaxAge: time.Hour, Now: func() time.Time { return sweepNow },
		Store: fakeStore{err: errors.New("db down")}}
	if n, err := sw.SweepOnce(context.Background()); err == nil || n != 0 {
		t.Fatalf("SweepOnce = %d, %v; want an error and no removals", n, err)
	}
	if !exists(p) {
		t.Fatal("archive removed although in-flight paths were unknown")
	}
}

func TestSweepGithubdStaging(t *testing.T) {
	root := t.TempDir()
	old := writeAged(t, root, "acct/app/abc123/source.tar.gz", stale)
	tmp := writeAged(t, root, "acct/app/abc124/.source-77.tar.gz", stale)
	recent := writeAged(t, root, "acct/app/def456/source.tar.gz", fresh)
	other := writeAged(t, root, "acct/app/abc125/notes.txt", stale)
	ageDirs(t, root, stale)
	sw := Sweeper{Root: root, Layout: GithubdStaging, MaxAge: 24 * time.Hour,
		Now: func() time.Time { return sweepNow }, Store: fakeStore{}}
	if n, err := sw.SweepOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("SweepOnce = %d, %v; want 2 removals", n, err)
	}
	if exists(old) || exists(tmp) || !exists(recent) || !exists(other) {
		t.Fatalf("old=%v tmp=%v recent=%v other=%v", exists(old), exists(tmp), exists(recent), exists(other))
	}
}
