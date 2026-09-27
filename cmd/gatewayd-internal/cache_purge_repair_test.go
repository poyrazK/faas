package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type responseCachePurgeRepairStoreFake struct {
	changes []state.ResponseCachePurgeChange
	err     error
}

func (f *responseCachePurgeRepairStoreFake) LatestResponseCachePurgeID(context.Context) (int64, error) {
	return 0, f.err
}
func (f *responseCachePurgeRepairStoreFake) ListResponseCachePurgesAfter(_ context.Context, after int64, limit int) ([]state.ResponseCachePurgeChange, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []state.ResponseCachePurgeChange
	for _, change := range f.changes {
		if change.ID > after {
			out = append(out, change)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (*responseCachePurgeRepairStoreFake) BootstrapGatewayResponseCachePurgeCursor(context.Context, string) (int64, error) {
	return 0, nil
}
func (*responseCachePurgeRepairStoreFake) UpsertGatewayResponseCachePurgeWatermark(context.Context, string, int64) error {
	return nil
}
func (*responseCachePurgeRepairStoreFake) PruneResponseCachePurgeChangeLog(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type responseCachePurgeRepairInvalidatorFake struct {
	apps  []string
	paths []string
	tags  []string
	err   error
	all   int
}

func (f *responseCachePurgeRepairInvalidatorFake) PurgeResponseCacheByApp(appID string) error {
	f.apps = append(f.apps, appID)
	return f.err
}
func (f *responseCachePurgeRepairInvalidatorFake) PurgeResponseCacheAll() error {
	f.all++
	return f.err
}
func (f *responseCachePurgeRepairInvalidatorFake) InvalidateResponseCacheByPath(appID, path string) error {
	f.paths = append(f.paths, appID+":"+path)
	return f.err
}
func (f *responseCachePurgeRepairInvalidatorFake) InvalidateResponseCacheByTag(appID, tag string) error {
	f.tags = append(f.tags, appID+":"+tag)
	return f.err
}

func TestRepairDurableResponseCachePurgesAppliesScopesAndAdvances(t *testing.T) {
	store := &responseCachePurgeRepairStoreFake{changes: []state.ResponseCachePurgeChange{
		{ID: 4, AppID: "app-1"},
		{ID: 5, AppID: "app-2", PathGlob: "/products/*"},
		{ID: 6, AppID: "app-3", Tag: "Product:42"},
	}}
	inv := new(responseCachePurgeRepairInvalidatorFake)
	lastID := int64(3)
	rows, err := repairDurableResponseCachePurges(context.Background(), store, inv, &lastID, nil)
	if err != nil || rows != 3 || lastID != 6 {
		t.Fatalf("repair = rows %d cursor %d err %v; want rows 3 cursor 6", rows, lastID, err)
	}
	if len(inv.apps) != 1 || inv.apps[0] != "app-1" || len(inv.paths) != 1 || inv.paths[0] != "app-2:/products/*" || len(inv.tags) != 1 || inv.tags[0] != "app-3:product:42" {
		t.Fatalf("invalidations: app %v paths %v tags %v", inv.apps, inv.paths, inv.tags)
	}
	rows, err = repairDurableResponseCachePurges(context.Background(), store, inv, &lastID, nil)
	if err != nil || rows != 0 || lastID != 6 {
		t.Fatalf("empty repair = rows %d cursor %d err %v; want rows 0 cursor 6", rows, lastID, err)
	}
}

func TestRepairDurableResponseCachePurgesDoesNotAdvanceOnFailure(t *testing.T) {
	wantErr := errors.New("shared cache unavailable")
	store := &responseCachePurgeRepairStoreFake{changes: []state.ResponseCachePurgeChange{{ID: 8, AppID: "app-1"}}}
	inv := &responseCachePurgeRepairInvalidatorFake{err: wantErr}
	lastID := int64(7)
	if _, err := repairDurableResponseCachePurges(context.Background(), store, inv, &lastID, nil); !errors.Is(err, wantErr) {
		t.Fatalf("repair error = %v, want %v", err, wantErr)
	}
	if lastID != 7 {
		t.Fatalf("cursor after failed shared-cache purge = %d, want 7", lastID)
	}
}

func TestRepairDurableResponseCachePurgesRejectsMalformedEntry(t *testing.T) {
	store := &responseCachePurgeRepairStoreFake{changes: []state.ResponseCachePurgeChange{{ID: 8, AppID: "app-1", Tag: "product:42", PathGlob: "/products/*"}}}
	inv := new(responseCachePurgeRepairInvalidatorFake)
	lastID := int64(7)
	if _, err := repairDurableResponseCachePurges(context.Background(), store, inv, &lastID, nil); err == nil {
		t.Fatal("malformed scope unexpectedly repaired")
	}
	if lastID != 7 || len(inv.apps)+len(inv.paths)+len(inv.tags) != 0 {
		t.Fatalf("cursor %d invalidations=%+v; malformed entry must not apply or advance", lastID, inv)
	}
}
