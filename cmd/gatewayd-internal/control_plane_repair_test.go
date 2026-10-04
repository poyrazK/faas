package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
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
	resetApps     []string
	routeApps     []string
	cacheApps     []string
	targetApps    []string
	weightApps    []string
	refreshes     []string
	targetError   error
	targetRefresh func(context.Context, string) error
	weightError   error
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

func (f *controlPlaneRepairInvalidatorFake) RefreshLiveTargets(ctx context.Context, appID string) error {
	f.targetApps = append(f.targetApps, appID)
	f.refreshes = append(f.refreshes, "targets:"+appID)
	if f.targetRefresh != nil {
		return f.targetRefresh(ctx, appID)
	}
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

// adr: 531
func TestRepairDurableControlPlaneChangesRecoversFromPeriodicPlacementCollision(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	app := "app-a"
	target := gateway.Target{AppID: app, InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
	started, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetPlacementLoader(func(readCtx context.Context, _ []string) (map[string]gateway.TargetPlacementSnapshot, error) {
		if reads.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-readCtx.Done():
				return nil, readCtx.Err()
			}
		}
		return map[string]gateway.TargetPlacementSnapshot{app: {AppID: app, Complete: true,
			Targets: []gateway.TargetPlacement{{Target: target, DeploymentLive: true}}}}, nil
	})
	backend.RecordTarget(app, target)
	store := &controlPlaneRepairStoreFake{changes: []state.ControlPlaneChange{{ID: 8, ResourceType: "deployment_traffic", AppID: app}}}
	cursor := int64(7)
	type result struct {
		rows int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := repairDurableControlPlaneChanges(ctx, store, backend, &cursor, nil)
		done <- result{rows: rows, err: err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("policy repair never began its placement read")
	}
	// The production periodic reconciler completes while policy repair holds
	// its older generation. That read must be discarded, then read afresh.
	if err := backend.ReconcileTargetPlacements(ctx); err != nil {
		t.Fatal(err)
	}
	if status := backend.TargetPlacementRefreshStatus(); !status.Succeeded || status.Checked != 1 {
		t.Fatalf("periodic placement refresh did not win: %+v", status)
	}
	close(release)
	select {
	case got := <-done:
		if got.err != nil || got.rows != 1 || cursor != 8 || reads.Load() != 3 {
			t.Fatalf("repair remained behind the periodic refresh: rows=%d cursor=%d reads=%d err=%v", got.rows, cursor, reads.Load(), got.err)
		}
	case <-ctx.Done():
		t.Fatal("policy repair did not finish within its original poll deadline")
	}
	if pick := backend.Pick(app); !pick.OK || pick.Target.InstanceID != target.InstanceID || backend.CapacityCount(app) != 1 {
		t.Fatalf("repair changed the resident placement: pick=%+v capacity=%d", pick, backend.CapacityCount(app))
	}
}

// adr: 531
func TestRepairDurableControlPlanePlacementRetryRetainsDeadlineAndCursor(t *testing.T) {
	collision := fmt.Errorf("placement read: %w", gateway.ErrTargetPlacementChanged)
	unavailable := errors.New("placement source unavailable")
	cases := []struct {
		name                   string
		first, second, wantErr error
		cancelAfterFirst       bool
		calls                  int
	}{
		{name: "fresh read succeeds", first: collision, calls: 2},
		{name: "second collision", first: collision, second: collision, wantErr: collision, calls: 2},
		{name: "source fails", first: unavailable, wantErr: unavailable, calls: 1},
		{name: "source fails after collision", first: collision, second: unavailable, wantErr: unavailable, calls: 2},
		{name: "caller cancels", first: collision, cancelAfterFirst: true, wantErr: context.Canceled, calls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			inv := &controlPlaneRepairInvalidatorFake{}
			calls := 0
			inv.targetRefresh = func(readCtx context.Context, _ string) error {
				calls++
				actualDeadline, ok := readCtx.Deadline()
				if readCtx != ctx || !ok || !actualDeadline.Equal(deadline) {
					t.Fatal("placement retry changed the original poll context or deadline")
				}
				if calls == 1 {
					if tc.cancelAfterFirst {
						cancel()
					}
					return tc.first
				}
				return tc.second
			}
			store := &controlPlaneRepairStoreFake{changes: []state.ControlPlaneChange{{ID: 8, ResourceType: "deployment_traffic", AppID: "app-a"}}}
			cursor := int64(7)
			rows, err := repairDurableControlPlaneChanges(ctx, store, inv, &cursor, nil)
			if calls != tc.calls || (err != nil) != (tc.wantErr != nil) || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("bounded retry: calls=%d err=%v; want calls=%d err=%v", calls, err, tc.calls, tc.wantErr)
			}
			if tc.wantErr != nil {
				if cursor != 7 || rows != 0 || len(inv.weightApps) != 0 {
					t.Fatalf("failed read advanced policy: cursor=%d rows=%d weights=%v", cursor, rows, inv.weightApps)
				}
			} else if cursor != 8 || rows != 1 || len(inv.weightApps) != 1 {
				t.Fatalf("fresh read did not advance policy: cursor=%d rows=%d weights=%v", cursor, rows, inv.weightApps)
			}
		})
	}
}
