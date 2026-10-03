package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type corsPresetRepairStoreFake struct {
	changes []state.CorsPresetChange
	err     error
}

func (f *corsPresetRepairStoreFake) LatestCorsPresetChangeID(context.Context) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if len(f.changes) == 0 {
		return 0, nil
	}
	return f.changes[len(f.changes)-1].ID, nil
}

func (f *corsPresetRepairStoreFake) ListCorsPresetChangesAfter(_ context.Context, afterID int64, limit int) ([]state.CorsPresetChange, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []state.CorsPresetChange
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

func (f *corsPresetRepairStoreFake) PruneCorsPresetChangeLog(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (f *corsPresetRepairStoreFake) UpsertGatewayCorsPresetWatermark(context.Context, string, string, int64) error {
	return nil
}

type corsPresetRepairInvalidatorFake struct {
	accounts []string
}

func (f *corsPresetRepairInvalidatorFake) ResetCorsPresets(accountID string) {
	f.accounts = append(f.accounts, accountID)
}

func TestRepairDurableCorsPresetChangesCoalescesAccounts(t *testing.T) {
	store := &corsPresetRepairStoreFake{changes: []state.CorsPresetChange{
		{ID: 1, AccountID: "account-b", PresetID: "preset-1", Operation: "created"},
		{ID: 2, AccountID: "account-a", PresetID: "preset-2", Operation: "updated"},
		{ID: 3, AccountID: "account-b", PresetID: "preset-1", Operation: "deleted"},
	}}
	inv := new(corsPresetRepairInvalidatorFake)
	lastID := int64(1)

	rows, err := repairDurableCorsPresetChanges(context.Background(), store, inv, &lastID, nil)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if rows != 2 || lastID != 3 {
		t.Fatalf("repair progress = rows %d, id %d; want 2, 3", rows, lastID)
	}
	if len(inv.accounts) != 2 || inv.accounts[0] != "account-a" || inv.accounts[1] != "account-b" {
		t.Fatalf("reset accounts = %v, want sorted unique [account-a account-b]", inv.accounts)
	}

	rows, err = repairDurableCorsPresetChanges(context.Background(), store, inv, &lastID, nil)
	if err != nil || rows != 0 || len(inv.accounts) != 2 {
		t.Fatalf("same page replay = rows %d, resets %v, err %v; want no-op", rows, inv.accounts, err)
	}
}

func TestRepairDurableCorsPresetChangesDoesNotAdvanceOnReadError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	store := &corsPresetRepairStoreFake{err: wantErr}
	inv := new(corsPresetRepairInvalidatorFake)
	lastID := int64(7)
	if _, err := repairDurableCorsPresetChanges(context.Background(), store, inv, &lastID, nil); !errors.Is(err, wantErr) {
		t.Fatalf("repair error = %v, want %v", err, wantErr)
	}
	if lastID != 7 || len(inv.accounts) != 0 {
		t.Fatalf("read error advanced cursor to %d or reset accounts %v", lastID, inv.accounts)
	}
}

func TestRepairDurableCorsPresetChangesDoesNotAdvanceOnMalformedEntry(t *testing.T) {
	store := &corsPresetRepairStoreFake{changes: []state.CorsPresetChange{
		{ID: 8, AccountID: "account-a"},
		{ID: 9, AccountID: "  "},
	}}
	inv := new(corsPresetRepairInvalidatorFake)
	lastID := int64(7)
	if _, err := repairDurableCorsPresetChanges(context.Background(), store, inv, &lastID, nil); err == nil {
		t.Fatal("repair with empty account ID succeeded")
	}
	if lastID != 7 || len(inv.accounts) != 0 {
		t.Fatalf("malformed page advanced cursor to %d or reset accounts %v", lastID, inv.accounts)
	}
}
