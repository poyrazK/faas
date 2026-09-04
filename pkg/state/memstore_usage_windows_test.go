package state

import (
	"context"
	"testing"
	"time"
)

func TestMemStoreUsageWindowsAggregatesCompletedHours(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	t0 := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	if err := m.AppendUsage(ctx, "acct-a", "app-a", "instance-a", t0.Add(5*time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUsage(ctx, "acct-a", "app-a", "instance-a", t0.Add(65*time.Minute), 200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUsage(ctx, "acct-b", "app-b", "instance-b", t0.Add(10*time.Minute), 300, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	got, err := m.UsageWindows(ctx, t0, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("UsageWindows returned %d rows, want 3: %+v", len(got), got)
	}
	want := []UsageWindow{
		{AccountID: "acct-a", Hour: t0, MBSeconds: 100},
		{AccountID: "acct-b", Hour: t0, MBSeconds: 300},
		{AccountID: "acct-a", Hour: t0.Add(time.Hour), MBSeconds: 200},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UsageWindows[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
