package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type edgeRuleRepairStoreFake struct {
	latest int64
	err    error
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
