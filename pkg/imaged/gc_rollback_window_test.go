package imaged

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPerAppKeepRollbackWindow_PreservesTiersPerDeployment(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var rows []state.SnapshotForGC
	for i := 0; i < 4; i++ {
		dep := "dep-" + string(rune('1'+i))
		created := t0.Add(time.Duration(i) * time.Minute)
		rows = append(rows,
			row("init-"+dep, "app", dep, "acct", "app", state.SnapshotTierInit, true, created, 1, 1),
			row("warm-"+dep, "app", dep, "acct", "app", state.SnapshotTierWarm, true, created.Add(time.Second), 1, 1),
		)
	}

	drops := perAppKeepRollbackWindow(rows, 3)
	ids := collectIDs(drops)
	want := []string{"init-dep-1", "warm-dep-1"}
	if !sortedStringsEqual(ids, want) {
		t.Fatalf("rollback window drops = %v, want %v", ids, want)
	}
}

func TestPerAppKeepRollbackWindow_DisabledDropsWarmRows(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var rows []state.SnapshotForGC
	for i := 0; i < 4; i++ {
		dep := "dep-" + string(rune('1'+i))
		created := t0.Add(time.Duration(i) * time.Minute)
		rows = append(rows,
			row("init-"+dep, "app", dep, "acct", "app", state.SnapshotTierInit, false, created, 1, 1),
			row("warm-"+dep, "app", dep, "acct", "app", state.SnapshotTierWarm, false, created.Add(time.Second), 1, 1),
		)
	}

	drops := perAppKeepRollbackWindow(rows, 3)
	ids := collectIDs(drops)
	want := []string{
		"init-dep-1",
		"warm-dep-1", "warm-dep-2", "warm-dep-3", "warm-dep-4",
	}
	if !sortedStringsEqual(ids, want) {
		t.Fatalf("disabled rollback window drops = %v, want %v", ids, want)
	}
}

func TestPerAppRollbackEvictionCandidates_ProtectsWindow(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]state.SnapshotForGC, 0, 4)
	for i := 0; i < 4; i++ {
		dep := "dep-" + string(rune('1'+i))
		rows = append(rows, row("snap-"+dep, "app", dep, "acct", "app",
			state.SnapshotTierWarm, true, t0.Add(time.Duration(i)*time.Minute), 1, 1))
	}

	candidates := perAppRollbackEvictionCandidates(rows, true, 3)
	if len(candidates) != 1 || candidates[0].ID != "snap-dep-1" {
		t.Fatalf("pressure candidates = %v, want only oldest protected-window miss", candidates)
	}
}

func TestPerAppRollbackEvictionCandidates_DropsTerminalRowsInsideWindow(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []state.SnapshotForGC{
		row("failed", "app", "dep-3", "acct", "app", state.SnapshotTierInit, true, t0.Add(2*time.Minute), 1, 1),
		row("live-2", "app", "dep-2", "acct", "app", state.SnapshotTierInit, true, t0.Add(time.Minute), 1, 1),
		row("live-1", "app", "dep-1", "acct", "app", state.SnapshotTierInit, true, t0, 1, 1),
	}
	rows[0].DeploymentStatus = state.DeployFailed
	candidates := perAppRollbackEvictionCandidates(rows, true, 3)
	if len(candidates) != 1 || candidates[0].ID != "failed" {
		t.Fatalf("terminal pressure candidates = %v, want failed row", candidates)
	}
}
