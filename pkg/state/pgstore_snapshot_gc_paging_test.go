//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The active GC projection was one newest-first page over the fleet, so past
// the limit it dropped the oldest generations GC exists to reclaim. It now
// reads whole apps page by page (bug hunt 2026-10-28 H66).
func TestPgSnapshotGCPagesActiveRowsByWholeApp(t *testing.T) {
	store, ctx, _ := pgWithPool(t)
	prev := *state.SnapshotGCPageRows
	*state.SnapshotGCPageRows = 3
	t.Cleanup(func() { *state.SnapshotGCPageRows = prev })

	acct, err := store.CreateAccount(ctx, "gc-paging-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{}
	for a := range 3 {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "gc-paging-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		for range a + 1 {
			dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "paging")}); err != nil {
				t.Fatal(err)
			}
			want[app.ID]++
		}
	}
	rows, err := store.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, row := range rows {
		if _, ours := want[row.AppID]; ours {
			got[row.AppID]++
		}
	}
	for appID, n := range want {
		if got[appID] != n {
			t.Fatalf("app %s: %d rows across pages, want %d (all: %v)", appID, got[appID], n, got)
		}
	}
}
