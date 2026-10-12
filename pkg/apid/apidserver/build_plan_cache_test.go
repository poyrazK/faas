// cmd/apid/build_plan_cache_test.go — tests for the in-process
// build-plan cache (HIGH-2 fix).
//
// Coverage:
//   - empty path returns unknown without touching the cache.
//   - missing file on disk returns unknown without panic.
//   - two consecutive calls on the same path hit the cache.
//   - mtime change invalidates the cache entry.
//
// The cache is process-local; the test resets it between runs so
// state from a sibling test does not bleed in.

package apidserver_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/markers"
)

func resetBuildPlanCache(t *testing.T) {
	t.Helper()
	(*apidserver.BuildPlanCacheOnce) = sync.Once{}
	(*apidserver.PlanCache) = nil
}

func TestGetCachedBuildPlan_EmptyPath(t *testing.T) {
	resetBuildPlanCache(t)
	fw, ver := apidserver.GetCachedBuildPlan("")
	if fw != markers.FrameworkUnknown || ver != "" {
		t.Errorf("empty path: got (%q, %q), want (unknown, \"\")", fw, ver)
	}
}

func TestGetCachedBuildPlan_MissingFile(t *testing.T) {
	resetBuildPlanCache(t)
	fw, ver := apidserver.GetCachedBuildPlan("/no/such/path/garbage.tar.gz")
	if fw != markers.FrameworkUnknown || ver != "" {
		t.Errorf("missing path: got (%q, %q), want (unknown, \"\")", fw, ver)
	}
}

func TestGetCachedBuildPlan_CachesAcrossCalls(t *testing.T) {
	resetBuildPlanCache(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.tar.gz")
	if err := os.WriteFile(path, []byte("not a real tarball"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Two calls — both should not panic. The first call stat's
	// the file; the second call hits the cache (assuming
	// DetectFromTarball returns unknown for an invalid tarball,
	// which it does — see pkg/markers/detect.go).
	for i := 0; i < 2; i++ {
		fw, _ := apidserver.GetCachedBuildPlan(path)
		if fw != markers.FrameworkUnknown {
			t.Errorf("call %d: expected unknown for invalid tarball, got %q", i, fw)
		}
	}
	(*(
	// Cache must be populated after the first call.
	*apidserver.PlanCache).Mu()).Lock()
	if _, ok := (*(*apidserver.PlanCache).Entries())[path]; !ok {
		t.Errorf("expected cache entry after first call")
	}
	(*(*apidserver.PlanCache).Mu()).Unlock()
}

func TestGetCachedBuildPlan_MtimeInvalidates(t *testing.T) {
	resetBuildPlanCache(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.tar.gz")
	initial := time.Now().Add(-time.Hour)
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(path, initial, initial); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// First call populates the cache with mtime=initial.
	_, _ = apidserver.GetCachedBuildPlan(path)

	// Rewrite the file with a newer mtime — should invalidate.
	newer := initial.Add(time.Minute)
	if err := os.Chtimes(path, newer, newer); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	_, _ = apidserver.GetCachedBuildPlan(path)
	(*(*

	// Confirm the cache holds the newer mtime.
	apidserver.PlanCache).Mu()).Lock()
	e, ok := (*(*apidserver.PlanCache).Entries())[path]
	if !ok {
		t.Fatal("expected cache entry")
	}
	if !(*e.Mtime()).Equal(newer) {
		t.Errorf("cache mtime = %v, want %v", (*e.Mtime()), newer)
	}
	(*(*apidserver.PlanCache).Mu()).Unlock()
}
