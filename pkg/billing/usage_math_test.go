package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOverageMBSecondsForRangeResetsAtUTCMonth(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "usage-math@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	august := time.Date(2026, 8, 1, 0, 5, 0, 0, time.UTC)
	windowStart := time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	// Exhaust August before the measured range, then use two billable
	// August GB-hours and three billable September GB-hours.
	rows := []struct {
		at time.Time
		mb int64
	}{
		{august, 50 * api.SecondsPerGBHour},
		{windowStart.Add(5 * time.Minute), 2 * api.SecondsPerGBHour},
		{windowStart.Add(time.Hour + 5*time.Minute), 53 * api.SecondsPerGBHour},
	}
	for i, row := range rows {
		if err := store.AppendUsage(ctx, acct.ID, "app-"+string(rune('a'+i)), "instance", row.at, row.mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := OverageMBSecondsForRange(ctx, store, acct, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(5) * api.SecondsPerGBHour; got != want {
		t.Fatalf("overage range = %d, want %d (5 GB-hours)", got, want)
	}
}
