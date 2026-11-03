package state_test

// adr: 20261102124306

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type appMemoryPressureTestStore = contractTestStore

func TestMemAppMemoryPressure(t *testing.T) {
	testAppMemoryPressure(t, state.NewMemStore())
}

// Reports within the window add up; a report after the window has run out
// starts a new one.
func testAppMemoryPressure(t *testing.T, store appMemoryPressureTestStore) {
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "memory-pressure-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "memory-pressure-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.AppMemoryPressure(ctx, app.ID); err != nil || ok {
		t.Fatalf("an app without reports: ok=%v err=%v", ok, err)
	}
	start := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		after                  time.Duration
		available, total, anon int
	}{
		{0, 30, 246, 190},
		{time.Hour, 20, 246, 210},
		{2 * time.Hour, 25, 246, 200},
	} {
		if err := store.RecordAppMemoryPressure(ctx, app.ID, start.Add(r.after), r.available, r.total, r.anon); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.AppMemoryPressure(ctx, app.ID)
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if got.Episodes != 3 || got.LowestAvailableMB != 20 || got.PeakAnonMB != 210 || got.TotalMB != 246 ||
		!got.WindowStartedAt.Equal(start) || !got.LastAt.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("rollup = %+v", got)
	}
	later := start.Add(api.AppHealthMemoryPressureWindow + time.Minute)
	if err := store.RecordAppMemoryPressure(ctx, app.ID, later, 60, 502, 300); err != nil {
		t.Fatal(err)
	}
	got, _, _ = store.AppMemoryPressure(ctx, app.ID)
	if got.Episodes != 1 || got.LowestAvailableMB != 60 || got.PeakAnonMB != 300 || got.TotalMB != 502 || !got.WindowStartedAt.Equal(later) {
		t.Fatalf("a report after the window = %+v; want a new window", got)
	}
	if err := store.RecordAppMemoryPressure(ctx, app.ID, later, -1, 502, 300); err == nil {
		t.Fatal("a negative value was recorded")
	}
}
