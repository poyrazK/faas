// adr: 641
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestWorkflowRunDueExistsCoversEveryClaimableRun pins the dispatch pre-check
// against the fair claim it guards: whenever a run is claimable the check
// reports due, and with nothing due the claim returns ErrNotFound without
// taking the fleet-wide fairness lock.
func TestWorkflowRunDueExistsCoversEveryClaimableRun(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool)
	stale := int64(WorkflowRunStaleAfter / time.Millisecond)
	due := func(step string) bool {
		t.Helper()
		v, err := sqlc.New().WorkflowRunDueExists(ctx, pool, stale)
		if err != nil {
			t.Fatalf("%s: WorkflowRunDueExists: %v", step, err)
		}
		return v
	}
	nothingClaimable := func(step string) {
		t.Helper()
		if due(step) {
			t.Fatalf("%s: reported due", step)
		}
		if run, err := store.ClaimNextDueWorkflowRun(ctx); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: claimed %+v (err %v), want ErrNotFound", step, run, err)
		}
	}
	setRun := func(id, set string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `update workflow_runs set `+set+` where id = $1`, id); err != nil {
			t.Fatalf("update run: %v", err)
		}
	}

	app, _ := seedWorkflowSchedule(t, store, "allow")
	nothingClaimable("empty")

	later := createFairDispatchRun(t, store, app, "", time.Now().Add(time.Hour))
	nothingClaimable("pending in the future")

	now := createFairDispatchRun(t, store, app, "", time.Now().Add(-time.Minute))
	if !due("pending and due") {
		t.Fatal("pending and due: not reported due")
	}
	claimFairDispatchRun(t, store, now)
	nothingClaimable("running with a live lease")

	setRun(now.ID, `lease_until = now() - interval '1 second'`)
	if !due("running with an expired lease") {
		t.Fatal("running with an expired lease: not reported due")
	}
	claimFairDispatchRun(t, store, now)

	setRun(now.ID, `status = 'succeeded', finished_at = now()`)
	setRun(later.ID, `status = 'awaiting_event', scheduled_for = now() - interval '1 second'`)
	if !due("awaiting an event past its deadline") {
		t.Fatal("awaiting an event past its deadline: not reported due")
	}
	claimFairDispatchRun(t, store, later)
}
