package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestUploadStateLockRemovesTerminalOrphan(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, err := uploadStatePath("fixture-identity")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockResumableUploadState(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("active digest lock missing: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal orphan lock remains: %v", err)
	}
}

func TestCleanupUploadCacheRemovesOrphansAndStaleState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now().UTC()
	fresh := writeUploadStateFixture(t, "fresh", now)
	stale := writeUploadStateFixture(t, "stale", now.Add(-8*24*time.Hour))
	dir, err := uploadStateDir()
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "orphan.json.lock")
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result, _, err := cleanupUploadCacheExclusive(context.Background(), uploadCachePolicy{MaxAge: 7 * 24 * time.Hour, MaxEntries: 256}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 || result.Kept != 1 {
		t.Fatalf("cleanup result = %+v, want removed=2 kept=1", result)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh resumable state removed: %v", err)
	}
	for _, path := range []string{stale, stale + ".lock", orphan} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("cleanup left %s: %v", path, err)
		}
	}
}

func TestUploadCacheDryRunAndConcurrencyGuard(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := uploadStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "legacy.json.lock")
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdUploadCacheCleanup([]string{"--dry-run"}); code != 0 {
		t.Fatalf("dry-run = %d", code)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("dry-run removed orphan: %v", err)
	}

	activePath := filepath.Join(dir, "active.json")
	active, err := lockResumableUploadState(context.Background(), activePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := cleanupUploadCacheExclusive(ctx, uploadCachePolicy{MaxAge: uploadCacheMaxAge, MaxEntries: uploadCacheMaxEntries}, true); err == nil {
		t.Fatal("cleanup acquired exclusive guard during an active upload")
	}
	if _, err := os.Stat(activePath + ".lock"); err != nil {
		t.Fatalf("active lock was removed: %v", err)
	}
	_ = active.Unlock()
	_ = active.Close()
}

func writeUploadStateFixture(t *testing.T, key string, created time.Time) string {
	t.Helper()
	dir, err := uploadStateDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, key+".json")
	if err := saveResumableUploadState(path, resumableUploadState{
		Version: resumableUploadStateVersion, UploadID: "upload-" + key, AppSlug: "demo",
		ArchivePath: "/tmp/source.tar.gz", ArchiveSize: 10, TotalSize: 10,
		CreatedAt: created.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReclaimUploadSessionsPreservesRecoveryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		uncertain, foreign, denied  bool
		wantRequests, wantReclaimed int
	}{
		{name: "unfinished", wantRequests: 1, wantReclaimed: 1},
		{name: "uncertain commit", uncertain: true},
		{name: "different API", foreign: true},
		{name: "cancellation denied", denied: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/uploads/old-upload" {
					t.Errorf("unexpected cleanup request: %s %s", r.Method, r.URL.Path)
				}
				if tc.denied {
					w.WriteHeader(http.StatusForbidden)
				} else {
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "old.json")
			base := srv.URL
			if tc.foreign {
				base = "https://another-api.example"
			}
			if err := saveResumableUploadState(path, resumableUploadState{
				Version: resumableUploadStateVersion, UploadID: "old-upload", AppSlug: "demo",
				APIBase: base, CommitAttempted: tc.uncertain, ArchiveSize: 1,
			}); err != nil {
				t.Fatal(err)
			}
			got := reclaimAppUploadSessions(t.Context(), api.NewClient(srv.URL, "test"), "demo", filepath.Join(filepath.Dir(path), "keep.json"))
			if got != tc.wantReclaimed || requests != tc.wantRequests {
				t.Fatalf("reclaimed=%d requests=%d", got, requests)
			}
			_, err := os.Stat(path)
			if tc.wantReclaimed == 0 && err != nil {
				t.Fatalf("recovery evidence lost: %v", err)
			}
			if tc.wantReclaimed == 1 && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancelled state remains: %v", err)
			}
		})
	}
}
