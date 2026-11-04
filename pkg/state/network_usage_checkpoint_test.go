package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestMemStoreAppendNetworkUsageObservationIsRestartSafe(t *testing.T) {
	store := state.NewMemStore()
	minute := time.Date(2026, 9, 11, 2, 10, 0, 0, time.UTC)
	const accountID = "account-network-checkpoint"
	const appID = "app-network-checkpoint"
	const instanceID = "instance-network-checkpoint"

	if err := store.AppendUsage(t.Context(), accountID, appID, instanceID, minute, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	tx, rx, err := store.AppendNetworkUsageObservation(t.Context(), accountID, appID, instanceID, minute, 100, true, 40, true)
	if err != nil || tx != 100 || rx != 40 {
		t.Fatalf("first observation = (%d, %d, %v), want (100, 40, nil)", tx, rx, err)
	}
	// Replaying the same cumulative value simulates restart-after-commit.
	tx, rx, err = store.AppendNetworkUsageObservation(t.Context(), accountID, appID, instanceID, minute, 100, true, 40, true)
	if err != nil || tx != 0 || rx != 0 {
		t.Fatalf("replay = (%d, %d, %v), want zero delta", tx, rx, err)
	}

	next := minute.Add(time.Minute)
	if err := store.AppendUsage(t.Context(), accountID, appID, instanceID, next, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	tx, rx, err = store.AppendNetworkUsageObservation(t.Context(), accountID, appID, instanceID, next, 150, true, 0, false)
	if err != nil || tx != 50 || rx != 0 {
		t.Fatalf("partial advance = (%d, %d, %v), want (50, 0, nil)", tx, rx, err)
	}

	last := next.Add(time.Minute)
	if err := store.AppendUsage(t.Context(), accountID, appID, instanceID, last, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	tx, rx, err = store.AppendNetworkUsageObservation(t.Context(), accountID, appID, instanceID, last, 175, true, 55, true)
	if err != nil || tx != 25 || rx != 15 {
		t.Fatalf("advance after partial source = (%d, %d, %v), want (25, 15, nil)", tx, rx, err)
	}

	rows, err := store.UsageByMonth(t.Context(), accountID, minute)
	if err != nil || len(rows) != 1 || rows[0].NetTxBytes != 175 || rows[0].NetRxBytes != 55 {
		t.Fatalf("monthly usage = (%+v, %v), want net tx/rx 175/55", rows, err)
	}
}

// adr: 20261101113742 — a vmmd or interface restart counts from zero again,
// so the first reading after it is new egress, not a regression to drop.
func TestMemStoreAppendNetworkUsageObservationCountsCounterReset(t *testing.T) {
	store := state.NewMemStore()
	minute := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	observe := func(at time.Time, tx, rx int64) (int64, int64) {
		t.Helper()
		if err := store.AppendUsage(t.Context(), "account", "app", "instance", at, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		dtx, drx, err := store.AppendNetworkUsageObservation(t.Context(), "account", "app", "instance", at, tx, true, rx, true)
		if err != nil {
			t.Fatal(err)
		}
		return dtx, drx
	}
	if tx, rx := observe(minute, 1_000, 400); tx != 1_000 || rx != 400 {
		t.Fatalf("first observation = (%d, %d), want (1000, 400)", tx, rx)
	}
	if tx, rx := observe(minute.Add(time.Minute), 30, 12); tx != 30 || rx != 12 {
		t.Fatalf("observation after a counter reset = (%d, %d), want the whole new value (30, 12)", tx, rx)
	}
	if tx, rx := observe(minute.Add(2*time.Minute), 80, 20); tx != 50 || rx != 8 {
		t.Fatalf("observation after the reset baseline = (%d, %d), want (50, 8)", tx, rx)
	}
	rows, err := store.UsageByMonth(t.Context(), "account", minute)
	if err != nil || len(rows) != 1 || rows[0].NetTxBytes != 1_080 || rows[0].NetRxBytes != 420 {
		t.Fatalf("monthly usage = (%+v, %v), want net tx/rx 1080/420", rows, err)
	}
}

func TestMemStoreAppendNetworkUsageObservationDoesNotAdvanceWithoutBaseRow(t *testing.T) {
	store := state.NewMemStore()
	minute := time.Date(2026, 9, 11, 2, 10, 0, 0, time.UTC)
	if _, _, err := store.AppendNetworkUsageObservation(t.Context(), "account", "app", "instance", minute, 100, true, 40, true); err == nil {
		t.Fatal("missing usage row: err=nil, want failure")
	}
	if err := store.AppendUsage(t.Context(), "account", "app", "instance", minute, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	tx, rx, err := store.AppendNetworkUsageObservation(t.Context(), "account", "app", "instance", minute, 100, true, 40, true)
	if err != nil || tx != 100 || rx != 40 {
		t.Fatalf("observation after base row = (%d, %d, %v), want full first delta", tx, rx, err)
	}
}
