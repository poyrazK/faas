package state_test

// adr: 20261028095548

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func backfillCount(t *testing.T, f backfillDeliveryFixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEventBackfillCancelReleasesUndeliveredEvents(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt-cancel-1", "evt-cancel-2", "evt-cancel-3")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)

	job, err := f.store.CancelEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "cancelled" || job.CompletedAt == nil || job.Progress.Enqueued != 1 || job.Progress.Cancelled != 2 || job.Progress.Pending != 0 || job.Progress.Processing != 0 {
		t.Fatalf("cancelled job = %+v", job)
	}
	if n := backfillCount(t, f, `SELECT count(*) FROM event_fanout_recipients WHERE backfill_job_id=$1 AND state IN ('pending','processing')`, f.job.ID); n != 0 {
		t.Fatalf("undelivered backfill recipients = %d, want 0", n)
	}
	if n := backfillCount(t, f, `SELECT count(*) FROM event_fanout_outbox WHERE account_id=$1 AND state<>'delivered'`, f.account); n != 0 {
		t.Fatalf("unsettled receipts after cancel = %d, want 0", n)
	}
	if _, err := f.store.ClaimDuePublishedEventRecipient(ctx, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim after cancel err=%v, want ErrNotFound", err)
	}
	if _, err := f.store.RetryFailedEventReplayBackfill(ctx, f.account, f.job.ID, 10); !errors.Is(err, state.ErrEventReplayBackfillState) {
		t.Fatalf("retry of a cancelled job err=%v", err)
	}
	again, err := f.store.CancelEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil || again.State != "cancelled" || !again.CompletedAt.Equal(*job.CompletedAt) {
		t.Fatalf("second cancel = %+v, %v", again, err)
	}
	// An older release's finalize step has no state guard; the trigger keeps
	// the job ended.
	if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET state='running',completed_at=NULL WHERE id=$1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID); err != nil || got.State != "cancelled" {
		t.Fatalf("ended job reopened: %+v, %v", got, err)
	}
	if _, err := f.store.CancelEventReplayBackfill(ctx, f.account, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancel of an unknown job err=%v", err)
	}
}

func TestEventBackfillExpiresWhenIdle(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt-idle-1")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	now := time.Now().UTC()
	if n, err := f.store.ExpireIdleEventReplayBackfills(ctx, now, 10); err != nil || n != 0 {
		t.Fatalf("moving job expired: n=%d err=%v", n, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET updated_at=$2 WHERE id=$1`, f.job.ID, now.Add(-api.EventReplayBackfillIdleExpiry-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.store.ExpireIdleEventReplayBackfills(ctx, now, 10); err != nil || n != 1 {
		t.Fatalf("idle job not expired: n=%d err=%v", n, err)
	}
	job, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil || job.State != "expired" || job.Progress.Cancelled != 1 {
		t.Fatalf("expired job = %+v, %v", job, err)
	}
	if n, err := f.store.ReleaseEndedEventReplayBackfills(ctx, 10); err != nil || n != 0 {
		t.Fatalf("nothing left to release: n=%d err=%v", n, err)
	}
}

func TestEventBackfillReleaseFinishesAfterInterruptedEnd(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt-release-1", "evt-release-2")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	// The end committed but its release never ran (crash, timeout).
	if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET state='cancelled',completed_at=clock_timestamp() WHERE id=$1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.store.ReleaseEndedEventReplayBackfills(ctx, 10); err != nil || n != 1 {
		t.Fatalf("release n=%d err=%v", n, err)
	}
	job, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil || job.Progress.Cancelled != 2 || job.Progress.Pending != 0 {
		t.Fatalf("released job = %+v, %v", job, err)
	}
	if n := backfillCount(t, f, `SELECT count(*) FROM event_fanout_outbox WHERE account_id=$1 AND state<>'delivered'`, f.account); n != 0 {
		t.Fatalf("unsettled receipts after release = %d, want 0", n)
	}
}

func TestEventBackfillFailingJobBacksOffThenFails(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt-fail-1")
	ctx := context.Background()
	other, _, err := f.store.UpsertEventSubscription(ctx, f.account, f.app, "orders", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthy := createDeliveryBackfill(t, f, other.ID)
	// A target snapshot that no longer validates fails every pass.
	if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET recipient='{"id":"broken"}' WHERE id=$1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	worked, err := f.store.ProcessNextEventReplayBackfill(ctx, now)
	var jobErr *state.EventJobError
	if !worked || !errors.As(err, &jobErr) || jobErr.JobID != f.job.ID || jobErr.Code != "invalid_job" || jobErr.Ended {
		t.Fatalf("first pass worked=%t err=%v", worked, err)
	}
	var delay float64
	if err := f.pool.QueryRow(ctx, `SELECT extract(epoch FROM next_attempt_at-$2)::float8 FROM event_replay_jobs WHERE id=$1`, f.job.ID, now).Scan(&delay); err != nil {
		t.Fatal(err)
	}
	if want := api.EventJobFailureBackoffMin.Seconds(); delay < want-0.01 || delay > want+0.01 {
		t.Fatalf("first backoff = %.3fs, want %.0fs", delay, want)
	}
	// The failing job waits; the next job in line goes ahead.
	if worked, err := f.store.ProcessNextEventReplayBackfill(ctx, now); !worked || err != nil {
		t.Fatalf("healthy job pass worked=%t err=%v", worked, err)
	}
	if n := backfillCount(t, f, `SELECT scanned_count FROM event_replay_jobs WHERE id=$1`, healthy.ID); n != 1 {
		t.Fatalf("healthy job scanned %d receipts, want 1", n)
	}
	for i := 2; i <= api.EventJobFailureLimit; i++ {
		if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET next_attempt_at=NULL WHERE id=$1`, f.job.ID); err != nil {
			t.Fatal(err)
		}
		_, err := f.store.ProcessNextEventReplayBackfill(ctx, now)
		if !errors.As(err, &jobErr) || jobErr.Ended != (i == api.EventJobFailureLimit) {
			t.Fatalf("pass %d err=%v", i, err)
		}
	}
	job, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil || job.State != "failed" || job.LastFailure != "invalid_job" || job.CompletedAt == nil {
		t.Fatalf("failed job = %+v, %v", job, err)
	}
}

func TestEventBackfillHoldsOnlyReceiptsAheadOfItsScan(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt-hold-1", "evt-hold-2")
	ctx := context.Background()
	rows, err := f.pool.Query(ctx, `SELECT id, created_at FROM event_fanout_outbox WHERE account_id=$1 ORDER BY created_at, id`, f.account)
	if err != nil {
		t.Fatal(err)
	}
	type receipt struct {
		id       int64
		accepted time.Time
	}
	var receipts []receipt
	for rows.Next() {
		var r receipt
		if err := rows.Scan(&r.id, &r.accepted); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, r)
	}
	rows.Close()
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d", len(receipts))
	}
	hold := func(r receipt) string {
		var reason string
		if err := f.pool.QueryRow(ctx, `SELECT event_receipt_retention_hold($1,$2,$3,now()-interval '30 days',now())`, f.account, r.id, r.accepted).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		return reason
	}
	if hold(receipts[0]) != "backfill_running" || hold(receipts[1]) != "backfill_running" {
		t.Fatal("a new backfill must hold its whole range")
	}
	// Move the cursor past the first receipt, as a scanned page does.
	if _, err := f.pool.Exec(ctx, `UPDATE event_replay_jobs SET cursor_at=$2,cursor_outbox_id=$3 WHERE id=$1`, f.job.ID, receipts[0].accepted, receipts[0].id); err != nil {
		t.Fatal(err)
	}
	if got := hold(receipts[0]); got != "" {
		t.Fatalf("scanned receipt hold = %q, want none", got)
	}
	if got := hold(receipts[1]); got != "backfill_running" {
		t.Fatalf("unscanned receipt hold = %q, want backfill_running", got)
	}
}

func TestEventRecoveryFailingJobBacksOffThenCancels(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedReplayPreviewApp(t, s)
	var jobID string
	if err := pool.QueryRow(ctx, `INSERT INTO event_recovery_jobs(account_id,app_id,selection,rate_per_second,expires_at)
		VALUES ($1,$2,'{"mode":"workflow","workflow_runs":{"name":"wf","runs":[]}}',10,now()+interval '1 day') RETURNING id::text`, account, app).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	// An item whose target no longer decodes fails every pass.
	if _, err := pool.Exec(ctx, `INSERT INTO event_recovery_items(job_id,position,outbox_id,subscription_id,event_source,event_id,event_type,failed_at,failure_code,retryable,expected_progress)
		VALUES ($1,1,NULL,'','','','',now(),'internal',true,'{"run_id":"00000000-0000-4000-8000-000000000001","expected_resume_count":0,"parent_position":"first"}')`, jobID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var jobErr *state.EventJobError
	for i := 1; i <= api.EventJobFailureLimit; i++ {
		worked, err := s.ProcessNextEventRecovery(ctx, now)
		if !worked || !errors.As(err, &jobErr) || jobErr.JobID != jobID || jobErr.Code != "internal" || jobErr.Ended != (i == api.EventJobFailureLimit) {
			t.Fatalf("pass %d worked=%t err=%v", i, worked, err)
		}
		if i == 1 {
			// Backed off: the next pass finds nothing due.
			if worked, err := s.ProcessNextEventRecovery(ctx, now); worked || err != nil {
				t.Fatalf("backed-off job processed again: worked=%t err=%v", worked, err)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE event_recovery_jobs SET next_attempt_at=$2 WHERE id=$1 AND state='running'`, jobID, now.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	var jobState, actor, reason string
	if err := pool.QueryRow(ctx, `SELECT j.state,h.actor_id,h.reason FROM event_recovery_jobs j
		JOIN event_recovery_history h ON h.job_id=j.id AND h.action='cancelled' WHERE j.id=$1`, jobID).Scan(&jobState, &actor, &reason); err != nil {
		t.Fatal(err)
	}
	if jobState != "cancelled" || actor != "recovery_failure" || reason != "processing failed 10 times in a row (last failure: internal)" {
		t.Fatalf("failing recovery ended as state=%q actor=%q reason=%q", jobState, actor, reason)
	}
}
