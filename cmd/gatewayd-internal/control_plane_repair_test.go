package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type controlPlaneRepairStoreFake struct {
	changes []state.ControlPlaneChange
	err     error
}

func (f *controlPlaneRepairStoreFake) LatestControlPlaneChangeID(context.Context) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if len(f.changes) == 0 {
		return 0, nil
	}
	return f.changes[len(f.changes)-1].ID, nil
}

func (f *controlPlaneRepairStoreFake) ListControlPlaneChangesAfter(_ context.Context, afterID int64, limit int) ([]state.ControlPlaneChange, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []state.ControlPlaneChange
	for _, change := range f.changes {
		if change.ID <= afterID {
			continue
		}
		out = append(out, change)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *controlPlaneRepairStoreFake) PruneControlPlaneChangeLog(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type controlPlaneRepairInvalidatorFake struct {
	resetApps []string
	cacheApps []string
}

func (f *controlPlaneRepairInvalidatorFake) ResetApp(appID string) {
	f.resetApps = append(f.resetApps, appID)
}

func (f *controlPlaneRepairInvalidatorFake) InvalidateResponseCacheByApp(appID string) {
	f.cacheApps = append(f.cacheApps, appID)
}

func TestRepairDurableControlPlaneChangesCoalescesApps(t *testing.T) {
	store := &controlPlaneRepairStoreFake{changes: []state.ControlPlaneChange{
		{ID: 1, ResourceType: "app", AppID: "app-a", Operation: "updated"},
		{ID: 2, ResourceType: "app", AppID: "app-a", Operation: "updated"},
		{ID: 3, ResourceType: "app", AppID: "app-b", Operation: "deleted"},
		{ID: 4, ResourceType: "future-resource", AppID: "app-b", Operation: "updated"},
	}}
	inv := new(controlPlaneRepairInvalidatorFake)
	lastID := int64(0)

	rows, err := repairDurableControlPlaneChanges(context.Background(), store, inv, &lastID, nil)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if rows != 4 || lastID != 4 {
		t.Fatalf("repair progress = rows %d, id %d; want 4, 4", rows, lastID)
	}
	if len(inv.resetApps) != 2 || inv.resetApps[0] != "app-a" || inv.resetApps[1] != "app-b" {
		t.Fatalf("reset apps = %v, want one reset per app", inv.resetApps)
	}
	if len(inv.cacheApps) != 2 || inv.cacheApps[0] != "app-a" || inv.cacheApps[1] != "app-b" {
		t.Fatalf("cache apps = %v, want one invalidation per app", inv.cacheApps)
	}
}

func TestRepairDurableControlPlaneChangesLeavesCursorOnError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	store := &controlPlaneRepairStoreFake{err: wantErr}
	inv := new(controlPlaneRepairInvalidatorFake)
	lastID := int64(7)

	if _, err := repairDurableControlPlaneChanges(context.Background(), store, inv, &lastID, nil); !errors.Is(err, wantErr) {
		t.Fatalf("repair error = %v, want %v", err, wantErr)
	}
	if lastID != 7 {
		t.Fatalf("cursor = %d, want unchanged 7", lastID)
	}
	if len(inv.resetApps) != 0 || len(inv.cacheApps) != 0 {
		t.Fatalf("invalidations after failed read = reset %v cache %v", inv.resetApps, inv.cacheApps)
	}
}
