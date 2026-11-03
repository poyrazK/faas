package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The hourly sweep keeps passing over the backlog until a batch comes back
// short, and leaves decisions inside the retention window alone.
func TestRunCronHistoryRetentionDrainsBacklogInPasses(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 1)
	c, err := store.CreateCron(ctx, app.ID, "* * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-api.CronHistoryRetention)
	seed := func(at time.Time) string {
		id := uuid.NewString()
		store.SeedScheduleOccurrenceForTest(state.ScheduleOccurrence{ID: id, AccountID: acct.ID, CronID: c.ID,
			ScheduleRevision: 1, ScheduledFor: at, Status: "succeeded"})
		return id
	}
	old := make([]string, 2*api.CronHistoryPruneBatch+5)
	for i := range old {
		old[i] = seed(cutoff.Add(-time.Duration(i+1) * time.Minute))
	}
	recent := seed(now.Add(-time.Hour))
	request := uuid.NewString()
	store.SeedFireNowRequestForTest(state.FireNowRequest{ID: request, CronID: c.ID, AccountID: acct.ID,
		RequestedAt: cutoff.Add(-time.Hour), Status: state.FireNowStatusSucceeded})

	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, sched.TestLog()).WithClock(func() time.Time { return now })
	loop.XRunCronHistoryRetention(ctx)

	for _, id := range old {
		if _, ok := store.ScheduleOccurrenceForTest(id); ok {
			t.Fatalf("occurrence %s older than the retention window survived the sweep", id)
		}
	}
	if _, ok := store.ScheduleOccurrenceForTest(recent); !ok {
		t.Fatal("the sweep deleted a decision inside the retention window")
	}
	if store.FireNowRequestForTest(request) {
		t.Fatal("the sweep kept a manual-run request older than the retention window")
	}
}
