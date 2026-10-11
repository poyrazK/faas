package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type edgeRuleRepairStoreFake struct {
	latest  int64
	err     error
	changes []state.EdgeRuleChange
}

func (f *edgeRuleRepairStoreFake) ListEdgeRuleChangesAfter(_ context.Context, afterID int64, limit int) ([]state.EdgeRuleChange, error) {
	var out []state.EdgeRuleChange
	for _, change := range f.changes {
		if change.ID > afterID && len(out) < limit {
			out = append(out, change)
		}
	}
	return out, nil
}

func (f *edgeRuleRepairStoreFake) LatestEdgeRuleChangeID(context.Context) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.latest, nil
}

func (f *edgeRuleRepairStoreFake) PruneEdgeRuleChangeLog(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (f *edgeRuleRepairStoreFake) UpsertGatewayEdgeRuleWatermark(context.Context, string, string, int64) error {
	return nil
}

type edgeRuleRepairInvalidatorFake struct {
	resetRules int
	resetCache int
}

func (f *edgeRuleRepairInvalidatorFake) ResetEdgeRules() {
	f.resetRules++
}

func (f *edgeRuleRepairInvalidatorFake) InvalidateResponseCacheAll() {
	f.resetCache++
}

func TestRepairDurableEdgeRuleChangesOnlyRepairsNewIDs(t *testing.T) {
	store := &edgeRuleRepairStoreFake{latest: 12}
	inv := &edgeRuleRepairInvalidatorFake{}
	lastID := int64(12)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	changed, err := repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, log)
	if err != nil {
		t.Fatalf("unchanged repair: %v", err)
	}
	if changed || inv.resetRules != 0 || inv.resetCache != 0 {
		t.Fatalf("unchanged ledger reset state = changed:%v rules:%d cache:%d", changed, inv.resetRules, inv.resetCache)
	}

	store.latest = 13
	changed, err = repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, log)
	if err != nil {
		t.Fatalf("new mutation repair: %v", err)
	}
	if !changed || lastID != 13 || inv.resetRules != 1 || inv.resetCache != 1 {
		t.Fatalf("new ledger reset state = changed:%v last:%d rules:%d cache:%d", changed, lastID, inv.resetRules, inv.resetCache)
	}

	changed, err = repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, log)
	if err != nil {
		t.Fatalf("replayed repair: %v", err)
	}
	if changed || inv.resetRules != 1 || inv.resetCache != 1 {
		t.Fatalf("same ledger was repaired twice: changed:%v rules:%d cache:%d", changed, inv.resetRules, inv.resetCache)
	}
}

type scopedRepairInvalidatorFake struct {
	edgeRuleRepairInvalidatorFake
	hosts []string
	apps  []string
	owner map[string]string // host -> app id
}

func (f *scopedRepairInvalidatorFake) InvalidateEdgeRuleHosts(hosts []string) {
	f.hosts = append(f.hosts, hosts...)
}

func (f *scopedRepairInvalidatorFake) InvalidateResponseCacheByApp(appID string) {
	f.apps = append(f.apps, appID)
}

func (f *scopedRepairInvalidatorFake) Lookup(_ context.Context, host string) (gateway.App, bool) {
	id, ok := f.owner[host]
	return gateway.App{ID: id}, ok
}

// One tenant's rule edits must not flush every other tenant's caches: a
// replayed change drops only its own hosts' rule sets and the response caches
// of the apps those hosts belong to. A wildcard host still flushes the
// response cache (it cannot be resolved to apps), and a backlog at the replay
// limit falls back to the wholesale flush.
func TestRepairDurableEdgeRuleChangesInvalidatesOnlyTheChangedScope(t *testing.T) {
	store := &edgeRuleRepairStoreFake{latest: 3, changes: []state.EdgeRuleChange{
		{ID: 2, AppID: "app-a", MatchHosts: []string{"api.a.example"}},
		{ID: 3, AppID: "app-a", MatchHosts: []string{"old.a.example", "www.b.example"}},
	}}
	inv := &scopedRepairInvalidatorFake{owner: map[string]string{"api.a.example": "app-a", "www.b.example": "app-b"}}
	lastID := int64(1)
	if _, err := repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, nil); err != nil {
		t.Fatal(err)
	}
	if inv.resetRules != 0 || inv.resetCache != 0 {
		t.Fatalf("scoped replay flushed wholesale: rules:%d cache:%d", inv.resetRules, inv.resetCache)
	}
	if lastID != 3 {
		t.Fatalf("lastID = %d, want 3", lastID)
	}
	wantHosts := []string{"api.a.example", "old.a.example", "www.b.example"}
	if !slices.Equal(inv.hosts, wantHosts) {
		t.Fatalf("invalidated hosts = %v, want %v", inv.hosts, wantHosts)
	}
	slices.Sort(inv.apps)
	if want := []string{"app-a", "app-a", "app-b"}; !slices.Equal(inv.apps, want) {
		t.Fatalf("invalidated response caches = %v, want %v", inv.apps, want)
	}

	store.latest = 4
	store.changes = append(store.changes, state.EdgeRuleChange{ID: 4, AppID: "app-c", MatchHosts: []string{"*.c.example"}})
	if _, err := repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, nil); err != nil {
		t.Fatal(err)
	}
	if inv.resetRules != 0 || inv.resetCache != 1 {
		t.Fatalf("wildcard change: rules:%d cache:%d, want rule cache scoped and response cache flushed", inv.resetRules, inv.resetCache)
	}

	store.changes = nil
	for id := int64(5); id < 5+edgeRuleRepairReplayLimit; id++ {
		store.changes = append(store.changes, state.EdgeRuleChange{ID: id, AppID: "app-a", MatchHosts: []string{"api.a.example"}})
	}
	store.latest = 4 + edgeRuleRepairReplayLimit
	if _, err := repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, nil); err != nil {
		t.Fatal(err)
	}
	if inv.resetRules != 1 || lastID != store.latest {
		t.Fatalf("large backlog: rules:%d last:%d, want wholesale flush to latest", inv.resetRules, lastID)
	}
}

func TestRepairDurableEdgeRuleChangesDoesNotAdvanceOnReadError(t *testing.T) {
	store := &edgeRuleRepairStoreFake{latest: 13, err: errors.New("database unavailable")}
	inv := &edgeRuleRepairInvalidatorFake{}
	lastID := int64(12)

	changed, err := repairDurableEdgeRuleChanges(context.Background(), store, inv, &lastID, slog.Default())
	if err == nil || changed {
		t.Fatalf("read error result = changed:%v err:%v, want unchanged error", changed, err)
	}
	if lastID != 12 || inv.resetRules != 0 || inv.resetCache != 0 {
		t.Fatalf("read error advanced repair state: last:%d rules:%d cache:%d", lastID, inv.resetRules, inv.resetCache)
	}
}
