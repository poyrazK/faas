package state

import (
	"testing"
	"time"
)

// TestPgWorkflowRunsDueAgreesWithClaim pins the check a scheduler tick shares
// across its workflow dispatch slots: false while the only run waits for a
// later schedule, true once a run is due, and that run is the one the claim
// takes.
func TestPgWorkflowRunsDueAgreesWithClaim(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		pg, ok := store.(*PgStore)
		if !ok {
			t.Skip("only the Postgres store answers the due check")
		}
		due := func() bool {
			t.Helper()
			got, err := pg.WorkflowRunsDue(t.Context())
			if err != nil {
				t.Fatalf("WorkflowRunsDue: %v", err)
			}
			return got
		}
		app, _ := seedWorkflowSchedule(t, store, "allow")
		now := time.Now().UTC().Truncate(time.Microsecond)
		if due() {
			t.Fatal("due with no workflow runs")
		}
		queueHealthRun(t, store, app, "later", "", now.Add(time.Hour), 0)
		if due() {
			t.Fatal("due while the only run is scheduled an hour out")
		}
		ready := queueHealthRun(t, store, app, "ready", "", now.Add(-time.Minute), 0)
		if !due() {
			t.Fatal("not due with a run past its schedule")
		}
		claimed, err := store.ClaimNextDueWorkflowRun(t.Context())
		if err != nil || claimed == nil || claimed.ID != ready.ID {
			t.Fatalf("claim = %+v, %v; want the due run", claimed, err)
		}
	})
}
