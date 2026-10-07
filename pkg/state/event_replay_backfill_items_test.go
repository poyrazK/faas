package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEventReplayBackfillItemsCursorAndSnapshotRetention(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedReplayPreviewApp(t, s)
	jobID, subscriptionID := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	acceptedAt := now.Add(-time.Minute)
	fromAt, cutoffAt, untilAt := acceptedAt.Add(-time.Hour), acceptedAt.Add(time.Hour), acceptedAt.Add(2*time.Hour)
	_, err := pool.Exec(ctx, `
INSERT INTO event_replay_jobs
    (id,account_id,app_id,subscription_id,subscription_revision,recipient,from_at,until_at,cutoff_at,cursor_at,state,scan_complete,completed_at)
VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,$6,$7,$8,$6,'completed',true,$9)`,
		jobID, accountID, appID, subscriptionID, strings.Repeat("a", 64), fromAt, untilAt, cutoffAt, now)
	if err != nil {
		t.Fatal(err)
	}
	longError := strings.Repeat("界", 500)
	for _, item := range []struct {
		outboxID  int64
		eventID   string
		accepted  time.Time
		state     string
		attempts  int
		code      string
		err       string
		retryable bool
	}{
		{101, "failed-1", acceptedAt, "failed", 3, "invocation_enqueue_failed", longError, true},
		{102, "failed-2", acceptedAt, "failed", 1, "", "", false},
		{103, "filtered-1", acceptedAt.Add(time.Minute), "filtered", 0, "", "", false},
	} {
		_, err := pool.Exec(ctx, `
INSERT INTO event_replay_job_items
    (job_id,outbox_id,accepted_at,event_source,event_id,event_type,schema_version,state,attempts,failure_code,last_error,retryable)
VALUES ($1,$2,$3,'orders.eu',$4,'invoice.paid','v2',$5,$6,$7,$8,$9)`,
			jobID, item.outboxID, item.accepted, item.eventID, item.state, item.attempts, item.code, item.err, item.retryable)
		if err != nil {
			t.Fatal(err)
		}
	}

	query := api.EventReplayBackfillItemsQuery{State: "failed", Limit: 1}
	first, err := s.ListEventReplayBackfillItems(ctx, accountID, jobID, query)
	if err != nil || first.JobID != jobID || len(first.Items) != 1 || first.NextAfter == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	item := first.Items[0]
	if item.EventID != "failed-1" || item.EventSource != "orders.eu" || item.EventType != "invoice.paid" || item.SchemaVersion != "v2" ||
		item.State != "failed" || item.Attempts != 3 || item.FailureCode != "invocation_enqueue_failed" || !item.Retryable ||
		len(item.LastError) > api.EventRoutingHistoryErrorMaxBytes || !item.DetailsTruncated || !item.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("first item=%+v", item)
	}

	query.After = first.NextAfter
	second, err := s.ListEventReplayBackfillItems(ctx, accountID, jobID, query)
	if err != nil || len(second.Items) != 1 || second.Items[0].EventID != "failed-2" || second.NextAfter != "" {
		t.Fatalf("second page=%+v err=%v", second, err)
	}

	query.State = "filtered"
	if _, err := s.ListEventReplayBackfillItems(ctx, accountID, jobID, query); !errors.Is(err, state.ErrEventReplayBackfillQuery) {
		t.Fatalf("cursor accepted a changed state filter: %v", err)
	}
	if _, err := s.ListEventReplayBackfillItems(ctx, accountID, uuid.NewString(), api.EventReplayBackfillItemsQuery{Limit: 1}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing job=%v", err)
	}
	otherAccount, _ := seedReplayPreviewApp(t, s)
	if _, err := s.ListEventReplayBackfillItems(ctx, otherAccount, jobID, api.EventReplayBackfillItemsQuery{Limit: 1}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign job=%v", err)
	}

	all, err := s.ListEventReplayBackfillItems(ctx, accountID, jobID, api.EventReplayBackfillItemsQuery{Limit: 10})
	if err != nil || len(all.Items) != 3 || all.Items[2].EventID != "filtered-1" || all.Items[2].State != "filtered" {
		t.Fatalf("all outcomes=%+v err=%v", all, err)
	}
	// No outbox rows were inserted in this fixture. The snapshotted identity
	// remains available independently of the source envelope's retention.
}
