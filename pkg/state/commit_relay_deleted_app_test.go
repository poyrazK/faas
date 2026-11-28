package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A deleted app's sources are not polled: the relay kept connecting to the
// customer's database for the whole seven-day restore window (async
// assessment 2026-11-28). Restoring the app resumes polling.
func TestPgCommitRelaySkipsDeletedApps(t *testing.T) {
	store, pool, existing, _ := commitManagedFixture(t)
	ctx := t.Context()
	source, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: existing.AccountID, AppID: existing.AppID, Name: "deleted-app"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("relay-fixture")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at=statement_timestamp()-interval '1 minute' WHERE app_id=$1::uuid`, existing.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, existing.AppID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimCommitRelaySources(ctx, 8, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("deleted app: got %d claims and error %v, want none", len(claimed), err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET status='active', deleted_at=NULL, delete_grace_until=NULL WHERE id=$1::uuid`, existing.AppID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimCommitRelaySources(ctx, 8, time.Minute)
	if err != nil || len(claimed) == 0 {
		t.Fatalf("restored app: got %d claims and error %v, want its sources", len(claimed), err)
	}
}
