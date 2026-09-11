package state

import (
	"testing"
	"time"
)

func TestMemStoreAppendNetworkUsageObservationIsRestartSafe(t *testing.T) {
	store := NewMemStore()
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

func TestMemStoreAppendNetworkUsageObservationDoesNotAdvanceWithoutBaseRow(t *testing.T) {
	store := NewMemStore()
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
