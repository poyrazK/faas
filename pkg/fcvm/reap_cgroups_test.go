// adr: 631
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestReapOrphanedTenantCgroups pins production-us hunt #4 (H4-61): every
// vmmd restart left an empty cgroup scope behind for each VM it recovered.
// The sweep removes only scopes that are aged, unowned and empty, and never
// a populated scope or a name that is not an instance id.
func TestReapOrphanedTenantCgroups(t *testing.T) {
	prev := rmdirCgroup
	rmdirCgroup = os.RemoveAll // a plain directory stands in for cgroupfs
	t.Cleanup(func() { rmdirCgroup = prev })

	root := t.TempDir()
	parent := "faas.slice/faas-tenant.slice/faas-tenant-scale.slice"
	old := time.Now().Add(-time.Hour)
	scope := func(name, populated string, mod time.Time, children ...string) string {
		t.Helper()
		dir := filepath.Join(root, parent, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte("populated "+populated+"\nfrozen 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, child := range children {
			if err := os.MkdirAll(filepath.Join(dir, child), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(dir, mod, mod); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	const (
		idBusy    = "cccccccc-3333-4333-8333-cccccccccccc"
		idYoung   = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
		idUnknown = "ffffffff-6666-4666-8666-ffffffffffff"
	)
	dead := scope(idDead, "0", old, "workload-main")
	live := scope(idLive, "0", old)
	busy := scope(idBusy, "1", old)
	young := scope(idYoung, "0", time.Now())
	unknown := scope(idUnknown, "0", old)
	notInstance := scope("init.scope", "0", old)

	rep, err := ReapOrphanedTenantCgroups(context.Background(), TenantCgroupReapOptions{
		Root:    root,
		Parents: []string{parent, "faas.slice/faas-tenant.slice/missing.slice"},
		IsLive: func(_ context.Context, id string) (bool, error) {
			if id == idUnknown {
				return false, errors.New("store unavailable")
			}
			return id == idLive, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := TenantCgroupReapReport{Scanned: 5, Reaped: 1, SkippedLive: 1, SkippedBusy: 1, SkippedYoung: 1, SkippedUnknown: 1}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	if _, err := os.Stat(dead); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unowned empty scope still exists: %v", err)
	}
	for _, dir := range []string{live, busy, young, unknown, notInstance} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(dir), err)
		}
	}
}

func TestReapOrphanedTenantCgroupsRequiresALivenessGate(t *testing.T) {
	if _, err := ReapOrphanedTenantCgroups(context.Background(), TenantCgroupReapOptions{Root: t.TempDir()}); err == nil {
		t.Fatal("swept without a liveness gate")
	}
}

func TestTenantCgroupParentsCoverEveryPlanSlice(t *testing.T) {
	parents := tenantCgroupParents()
	want := map[string]bool{
		"faas.slice/faas-tenant.slice/faas-tenant-scale.slice": false,
		"faas.slice/faas-tenant.slice/faas-tenant-free.slice":  false,
	}
	for _, p := range parents {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("%s not swept; parents = %v", p, parents)
		}
	}
}
