package state

import (
	"testing"
	"time"
)

func TestMemStoreUsageDailyForAccountAggregatesAndBounds(t *testing.T) {
	store := NewMemStore()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	previous := today.AddDate(0, 0, -1)

	if err := store.AppendUsage(t.Context(), "acct", "app-a", "inst-a", today.Add(time.Hour), 100, 2, 3, 4, 5, 6, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(t.Context(), "acct", "app-a", "inst-a-2", today.Add(2*time.Hour), 200, 3, 4, 5, 6, 7, 2, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(t.Context(), "acct", "app-b", "inst-b", today.Add(time.Hour), 300, 4, 5, 6, 7, 8, 3, 9); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(t.Context(), "acct", "app-a", "inst-a-old", previous.Add(time.Hour), 400, 5, 6, 7, 8, 9, 4, 10); err != nil {
		t.Fatal(err)
	}

	rows, err := store.UsageDailyForAccount(t.Context(), "acct")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3 app/day rows", rows)
	}
	if !rows[0].Day.Equal(previous) || rows[0].AppID != "app-a" {
		t.Fatalf("first row = %+v, want previous/app-a", rows[0])
	}
	if rows[1].AppID != "app-a" || rows[1].MBSeconds != 300 || rows[1].Requests != 5 || rows[1].CPUUsec != 7 || rows[1].ColdBootCount != 3 {
		t.Fatalf("app-a today = %+v, want aggregated values", rows[1])
	}
	if rows[2].AppID != "app-b" || rows[2].MBSeconds != 300 {
		t.Fatalf("app-b today = %+v, want 300 mb-seconds", rows[2])
	}
}
