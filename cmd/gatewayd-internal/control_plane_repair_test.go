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

func (f *controlPlaneRepairStoreFake) UpsertGatewayControlPlaneWatermark(context.Context, string, string, int64) error {
	return nil
}

type controlPlaneRepairInvalidatorFake struct {
	resetApps   []string
	routeApps   []string
	cacheApps   []string
	targetApps  []string
	weightApps  []string
	refreshes   []string
	targetError error
	weightError error
}

func (f *controlPlaneRepairInvalidatorFake) ResetApp(appID string) {
	f.resetApps = append(f.resetApps, appID)
}

func (f *controlPlaneRepairInvalidatorFake) InvalidateResponseCacheByApp(appID string) {
	f.cacheApps = append(f.cacheApps, appID)
}

func (f *controlPlaneRepairInvalidatorFake) InvalidateRoutesForApp(appID string) {
	f.routeApps = append(f.routeApps, appID)
}

func (f *controlPlaneRepairInvalidatorFake) RefreshLiveTargets(_ context.Context, appID string) error {
	f.targetApps = append(f.targetApps, appID)
	f.refreshes = append(f.refreshes, "targets:"+appID)
	return f.targetError
}

func (f *controlPlaneRepairInvalidatorFake) RefreshDeploymentWeights(_ context.Context, appID string) error {
	f.weightApps = append(f.weightApps, appID)
	f.refreshes = append(f.refreshes, "weights:"+appID)
	return f.weightError
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
	if len(inv.routeApps) != 2 || inv.routeApps[0] != "app-a" || inv.routeApps[1] != "app-b" {
		t.Fatalf("route apps = %v, want one invalidation per app", inv.routeApps)
	}
	if len(inv.weightApps) != 0 || len(inv.targetApps) != 0 {
		t.Fatalf("app-only changes refreshed deployments: targets %v weights %v", inv.targetApps, inv.weightApps)
	}
}

func TestRepairDurableControlPlaneChangesRefreshesTrafficOncePerApp(t *testing.T) {
	store := &controlPlaneRepairStoreFake{changes: []state.ControlPlaneChange{
		{ID: 8, ResourceType: "deployment_traffic", AppID: "app-a", Operation: "updated"},
		{ID: 9, ResourceType: "deployment_traffic", AppID: "app-a", Operation: "updated"},
	}}
	inv := new(controlPlaneRepairInvalidatorFake)
	lastID := int64(7)
	rows, err := repairDurableControlPlaneChanges(context.Background(), store, inv, &lastID, nil)
	if err != nil || rows != 2 || lastID != 9 {
		t.Fatalf("repair = rows %d, cursor %d, err %v; want 2, 9, nil", rows, lastID, err)
	}
	if len(inv.targetApps) != 1 || inv.targetApps[0] != "app-a" || len(inv.weightApps) != 1 || inv.weightApps[0] != "app-a" {
		t.Fatalf("traffic refresh = targets %v weights %v; want app-a once each", inv.targetApps, inv.weightApps)
	}
	if len(inv.refreshes) != 2 || inv.refreshes[0] != "targets:app-a" || inv.refreshes[1] != "weights:app-a" {
		t.Fatalf("refresh order = %v, want targets before weights", inv.refreshes)
	}
	if len(inv.cacheApps) != 1 || inv.cacheApps[0] != "app-a" {
		t.Fatalf("cache invalidations = %v; want app-a", inv.cacheApps)
	}
}

func TestRepairDurableControlPlaneChangesRetriesFailedTrafficRefresh(t *testing.T) {
	store := &controlPlaneRepairStoreFake{changes: []state.ControlPlaneChange{
		{ID: 8, ResourceType: "deployment_traffic", AppID: "app-a", Operation: "updated"},
	}}
	wantErr := errors.New("weight store unavailable")
	inv := &controlPlaneRepairInvalidatorFake{weightError: wantErr}
	lastID := int64(7)
	if _, err := repairDurableControlPlaneChanges(context.Background(), store, inv, &lastID, nil); !errors.Is(err, wantErr) {
		t.Fatalf("refresh error = %v, want %v", err, wantErr)
	}
	if lastID != 7 {
		t.Fatalf("cursor = %d, want unchanged 7", lastID)
	}
	inv.weightError = nil
	if rows, err := repairDurableControlPlaneChanges(context.Background(), store, inv, &lastID, nil); err != nil || rows != 1 || lastID != 8 {
		t.Fatalf("retry = rows %d, cursor %d, err %v; want 1, 8, nil", rows, lastID, err)
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
