package state_test

// adr: 646

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type backfillDeliveryFixture struct {
	store                      *state.PgStore
	pool                       *pgxpool.Pool
	account, app, subscription string
	job                        api.EventReplayBackfillJobResponse
}

func publishBackfillDeliveryEvent(t *testing.T, s *state.PgStore, account, id string) {
	t.Helper()
	envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "invoice.paid", Data: json.RawMessage(`{"amount":150}`)}).Normalize(account, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(context.Background(), "apid", "event.published", &account, payload); err != nil {
		t.Fatal(err)
	}
}

func seedBackfillDeliveries(t *testing.T, ids ...string) backfillDeliveryFixture {
	return seedBackfillDeliveriesWithSnapshot(t, false, ids...)
}

func seedBackfillDeliveriesWithSnapshot(t *testing.T, original bool, ids ...string) backfillDeliveryFixture {
	t.Helper()
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedReplayPreviewApp(t, s)
	if original {
		if _, _, err := s.UpsertEventSubscription(ctx, account, app, "*", "*", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		publishBackfillDeliveryEvent(t, s, account, id)
		work, err := s.ClaimDuePublishedEvent(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
	}
	sub, _, err := s.UpsertEventSubscription(ctx, account, app, "orders", "invoice.paid", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := backfillDeliveryFixture{store: s, pool: pool, account: account, app: app, subscription: sub.ID}
	f.job = createDeliveryBackfill(t, f, sub.ID)
	return f
}

func createDeliveryBackfill(t *testing.T, f backfillDeliveryFixture, sub string) api.EventReplayBackfillJobResponse {
	t.Helper()
	job, err := f.store.CreateEventReplayBackfill(context.Background(), f.account, state.EventReplayBackfillQuery{
		AppID: f.app, SubscriptionID: sub, EventReplayBackfillRequest: api.EventReplayBackfillRequest{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func scanDeliveryBackfill(t *testing.T, f backfillDeliveryFixture) {
	t.Helper()
	for range 2 {
		worked, err := f.store.ProcessNextEventReplayBackfill(context.Background(), time.Now())
		if err != nil || !worked {
			t.Fatalf("scan worked=%t error=%v", worked, err)
		}
	}
}

func admitDeliveryBackfill(t *testing.T, f backfillDeliveryFixture) string {
	t.Helper()
	ctx := context.Background()
	work, err := f.store.ClaimDuePublishedEventRecipient(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.AdmitPublishedEventRecipient(ctx, state.PublishedEventRoutingClaim{
		OutboxID: work.OutboxID, SubscriptionID: work.Recipient.ID, ClaimToken: work.ClaimToken, Generation: work.Generation, BackfillJobID: work.BackfillJobID,
	})
	if err != nil || !result.InvocationCreated {
		t.Fatalf("admission=%+v error=%v", result, err)
	}
	return result.DeliveryID
}

func readBackfillReceipt(t *testing.T, f backfillDeliveryFixture, id string) state.EventReceipt {
	t.Helper()
	r, err := f.store.EventReceipt(context.Background(), f.account, "orders", id, state.EventReceiptCursor{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEventBackfillDeliveryInspectionAndHandlerRecovery(t *testing.T) {
	f := seedBackfillDeliveries(t, "evt/?+&")
	ctx := context.Background()
	if n, err := f.store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(time.Hour), 100); err != nil || n != 0 {
		t.Fatalf("active job retention=%d %v", n, err)
	}
	scanDeliveryBackfill(t, f)
	root := admitDeliveryBackfill(t, f)
	job, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID)
	if err != nil || job.State != "completed" || job.Progress.Enqueued != 1 {
		t.Fatalf("job=%+v %v", job, err)
	}
	r := readBackfillReceipt(t, f, "evt/?+&")
	if r.RecipientCount != 0 || len(r.RoutingSummary) != 0 || r.BackfillRecipientCount != 1 || r.BackfillRoutingSummary["enqueued"] != 1 || len(r.Recipients) != 1 {
		t.Fatalf("snapshot and backfill counts=%+v", r)
	}
	entry := r.Recipients[0]
	if entry.Origin != "backfill" || entry.BackfillJobID != job.ID || entry.Execution == nil || entry.Execution.InvocationID != root || entry.Execution.State != "pending" {
		t.Fatalf("job completion must not imply handler completion: %+v", entry)
	}
	items, err := f.store.ListEventReplayBackfillItems(ctx, f.account, job.ID, api.EventReplayBackfillItemsQuery{})
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("items=%+v %v", items, err)
	}
	for _, link := range []string{items.Items[0].ReceiptURL, items.Items[0].AttemptHistoryURL} {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Query().Get("id") != "evt/?+&" || parsed.Query().Get("source") != "orders" {
			t.Fatalf("link=%q %v", link, err)
		}
	}
	failReceiptInvocation(t, f.store, root)
	r = readBackfillReceipt(t, f, "evt/?+&")
	if r.Recipients[0].HandlerReplayMode != "handler_replay" {
		t.Fatalf("handler recovery unavailable: %+v", r)
	}
	replay, err := f.store.EnqueueInvocation(ctx, state.Invocation{AccountID: f.account, AppID: f.app, Source: state.InvocationReplay, ReplayedFromInvocationID: root, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimInvocation(ctx, replay.ID, "backfill-test", 30); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteInvocation(ctx, replay.ID, nil); err != nil {
		t.Fatal(err)
	}
	r = readBackfillReceipt(t, f, "evt/?+&")
	entry = r.Recipients[0]
	if entry.Execution.State != "failed" || entry.Recovery == nil || entry.Recovery.LatestReplay.State != "completed" || entry.HandlerReplayMode != "" {
		t.Fatalf("original failure and recovered handler=%+v", entry)
	}
	history, err := f.store.EventReceiptReplays(ctx, f.account, "orders", "evt/?+&", f.subscription, state.EventReceiptReplayCursor{}, 100)
	if err != nil || len(history.Replays) != 1 || history.Replays[0].InvocationID != replay.ID {
		t.Fatalf("replay history=%+v %v", history, err)
	}
	attempts, err := f.store.EventReceiptAttempts(ctx, f.account, "orders", "evt/?+&", f.subscription, state.EventReceiptAttemptCursor{}, 100)
	if err != nil || len(attempts.Attempts) != 2 {
		t.Fatalf("handler attempts=%+v %v", attempts, err)
	}
	for _, probe := range []struct{ account, source, sub string }{{uuid.NewString(), "orders", f.subscription}, {f.account, "foreign", f.subscription}, {f.account, "orders", uuid.NewString()}} {
		if _, err := f.store.EventReceiptAttempts(ctx, probe.account, probe.source, "evt/?+&", probe.sub, state.EventReceiptAttemptCursor{}, 100); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign history=%v", err)
		}
	}
	createDeliveryBackfill(t, f, f.subscription)
	scanDeliveryBackfill(t, f)
	invocations, err := f.store.ListInvocationsForApp(ctx, f.app)
	if err != nil || len(invocations) != 2 {
		t.Fatalf("repeated backfill duplicated delivery: %+v %v", invocations, err)
	}
}

func TestEventBackfillSelectiveRoutingRecovery(t *testing.T) {
	for _, selective := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch_after_completion", true: "selective_while_sibling_pending"}[selective], func(t *testing.T) {
			ids := []string{"failed"}
			if selective {
				ids = append(ids, "pending")
			}
			f := seedBackfillDeliveries(t, ids...)
			ctx := context.Background()
			scanDeliveryBackfill(t, f)
			work, err := f.store.ClaimDuePublishedEventRecipient(ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			progress := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: work.TotalAttempts, UpdatedAt: time.Now(), FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, LastError: "transient enqueue failure"}
			if err := f.store.FinishPublishedEventRecipient(ctx, work, progress, time.Now()); err != nil {
				t.Fatal(err)
			}
			if r := readBackfillReceipt(t, f, "failed"); !r.Recipients[0].RoutingReplayEligible {
				t.Fatalf("selective recovery unavailable: %+v", r)
			}
			if selective {
				for _, probe := range []struct{ account, app string }{{uuid.NewString(), f.app}, {f.account, uuid.NewString()}} {
					if err := f.store.ReplayFailedPublishedEventRecipientForApp(ctx, probe.account, probe.app, "orders", "failed", f.subscription); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("foreign recovery=%v", err)
					}
				}
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for range 2 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						results <- f.store.ReplayFailedPublishedEventRecipientForApp(ctx, f.account, f.app, "orders", "failed", f.subscription)
					}()
				}
				wg.Wait()
				close(results)
				wins := 0
				for err := range results {
					if err == nil {
						wins++
					} else if !errors.Is(err, state.ErrNotFound) {
						t.Fatal(err)
					}
				}
				if wins != 1 {
					t.Fatalf("concurrent recovery winners=%d", wins)
				}
			} else {
				retry, err := f.store.RetryFailedEventReplayBackfill(ctx, f.account, f.job.ID, 1)
				if err != nil || retry.RetriedCount != 1 || retry.Job.State != "running" {
					t.Fatalf("retry=%+v %v", retry, err)
				}
			}
			r := readBackfillReceipt(t, f, "failed")
			if r.RoutingSettledAt != nil || r.Recipients[0].Routing.State != "pending" || *r.Recipients[0].Routing.Generation != 2 || r.Recipients[0].Routing.ReplayCount != 1 {
				t.Fatalf("reopened receipt=%+v", r)
			}
			for range len(ids) {
				admitDeliveryBackfill(t, f)
			}
			job, err := f.store.GetEventReplayBackfill(ctx, f.account, f.job.ID)
			if err != nil || job.State != "completed" || job.Progress.Enqueued != int64(len(ids)) {
				t.Fatalf("recovered job=%+v %v", job, err)
			}
		})
	}
}

func TestEventBackfillReceiptPaginationAndJobRetention(t *testing.T) {
	f := seedBackfillDeliveriesWithSnapshot(t, true, "pagination")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)
	first := readBackfillReceipt(t, f, "pagination")
	if first.RecipientCount != 1 || first.RoutingSummary["enqueued"] != 1 || first.Recipients[0].Origin != "acceptance" || first.Recipients[1].Position != 2 {
		t.Fatalf("first position=%+v", first)
	}
	for _, pattern := range []string{"*", "ord*"} {
		sub, _, err := f.store.UpsertEventSubscription(ctx, f.account, f.app, pattern, "invoice.paid", nil)
		if err != nil {
			t.Fatal(err)
		}
		createDeliveryBackfill(t, f, sub.ID)
		scanDeliveryBackfill(t, f)
		admitDeliveryBackfill(t, f)
	}
	page, err := f.store.EventReceipt(ctx, f.account, "orders", "pagination", state.EventReceiptCursor{}, 1)
	if err != nil || page.NextPosition != 1 || page.BackfillRecipientCount != 3 {
		t.Fatalf("first page=%+v %v", page, err)
	}
	if n, err := f.store.PruneEventReplayBackfills(ctx, time.Now().Add(31*24*time.Hour), 100); err != nil || n != 3 {
		t.Fatalf("job pruning=%d %v", n, err)
	}
	remaining, err := f.store.EventReceipt(ctx, f.account, "orders", "pagination", state.EventReceiptCursor{OutboxID: page.OutboxID, Position: page.NextPosition}, 100)
	if err != nil || len(remaining.Recipients) != 3 || remaining.Recipients[0].Position != 2 || remaining.Recipients[2].Position != 4 {
		t.Fatalf("stable continuation=%+v %v", remaining, err)
	}
	for _, entry := range remaining.Recipients {
		if entry.Origin != "backfill" || entry.BackfillJobID != "" || entry.Execution == nil {
			t.Fatalf("expired job changed provenance=%+v", entry)
		}
	}
}

func TestEventBackfillReceiptPositionMigration(t *testing.T) {
	f := seedBackfillDeliveriesWithSnapshot(t, true, "migration")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)
	// The old schema can retain added recipients after their job FK is cleared.
	if n, err := f.store.PruneEventReplayBackfills(ctx, time.Now().Add(31*24*time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("job prune=%d %v", n, err)
	}
	// Unwind the newer projection before restoring the legacy schema under test.
	projection, err := migrations.FS.ReadFile("20261008075254103_event_backlog_consumer_origins.sql")
	if err != nil {
		t.Fatal(err)
	}
	projectionParts := strings.SplitN(string(projection), "-- +goose Down", 2)
	if _, err := f.pool.Exec(ctx, projectionParts[1]); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261007231622122_event_backfill_receipt_positions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := f.pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	// Restore the current projection before querying through the current store.
	if _, err := f.pool.Exec(ctx, projectionParts[0]); err != nil {
		t.Fatal(err)
	}
	r := readBackfillReceipt(t, f, "migration")
	if r.RecipientCount != 1 || r.BackfillRecipientCount != 1 || r.Recipients[0].Origin != "acceptance" || r.Recipients[1].Origin != "backfill" || r.Recipients[1].Position != 2 {
		t.Fatalf("legacy positions=%+v", r)
	}
	// Replaying after receipt positions have advanced must preserve them, even
	// when a partially applied schema still has an unpositioned recipient.
	if _, err := f.pool.Exec(ctx, "UPDATE event_fanout_recipients SET receipt_position=9 WHERE subscription_id=$1", f.subscription); err != nil {
		t.Fatal(err)
	}
	sub, _, err := f.store.UpsertEventSubscription(ctx, f.account, f.app, "*", "invoice.paid", nil)
	if err != nil {
		t.Fatal(err)
	}
	createDeliveryBackfill(t, f, sub.ID)
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)
	if _, err := f.pool.Exec(ctx, "UPDATE event_fanout_recipients SET receipt_position=NULL WHERE subscription_id=$1", sub.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.pool.Exec(ctx, sections[0]); err != nil {
			t.Fatal(err)
		}
		r = readBackfillReceipt(t, f, "migration")
		if r.BackfillRecipientCount != 2 || len(r.Recipients) != 3 || r.Recipients[1].Position != 9 || r.Recipients[2].Position != 10 {
			t.Fatalf("replayed positions=%+v", r)
		}
	}
}

func TestEventBackfillInspectionAfterAppTransfer(t *testing.T) {
	f := seedBackfillDeliveries(t, "transferred")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)
	other, err := f.store.CreateAccount(ctx, uuid.NewString()+"@backfill-owner.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE apps SET account_id=$1, slug='private-new-owner' WHERE id=$2", other.ID, f.app); err != nil {
		t.Fatal(err)
	}
	r := readBackfillReceipt(t, f, "transferred")
	entry := r.Recipients[0]
	if entry.AppSlug != "" || entry.Execution != nil || entry.Recovery != nil || entry.RoutingReplayEligible || entry.HandlerReplayMode != "" {
		t.Fatalf("foreign app evidence=%+v", entry)
	}
	items, err := f.store.ListEventReplayBackfillItems(ctx, f.account, f.job.ID, api.EventReplayBackfillItemsQuery{})
	if err != nil || len(items.Items) != 1 || items.Items[0].ReceiptURL == "" || items.Items[0].AttemptHistoryURL != "" {
		t.Fatalf("foreign history links=%+v %v", items, err)
	}
	if _, err := f.store.EventReceiptAttempts(ctx, f.account, "orders", "transferred", f.subscription, state.EventReceiptAttemptCursor{}, 100); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign handler history=%v", err)
	}
}

func TestEventBackfillDeadLetterRecovery(t *testing.T) {
	f := seedBackfillDeliveries(t, "dead-letter")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	id := admitDeliveryBackfill(t, f)
	if _, err := f.store.ClaimInvocation(ctx, id, "backfill-dlq-test", 30); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FailInvocation(ctx, id, "handler exhausted", time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}
	dlq, err := f.store.ListDeadLetterEvents(ctx, f.app, 10, "")
	if err != nil || len(dlq) != 1 {
		t.Fatalf("dead letters=%+v %v", dlq, err)
	}
	r := readBackfillReceipt(t, f, "dead-letter")
	entry := r.Recipients[0]
	if entry.Execution.State != string(state.InvocationDeadLetter) || entry.HandlerReplayMode != "dead_letter_replay" || entry.HandlerReplayDeadLetterID != dlq[0].ID {
		t.Fatalf("backfill DLQ recovery=%+v", entry)
	}
}

func TestEventBackfillItemsDoNotLinkReusedIdentity(t *testing.T) {
	f := seedBackfillDeliveries(t, "reused")
	ctx := context.Background()
	scanDeliveryBackfill(t, f)
	admitDeliveryBackfill(t, f)
	if n, err := f.store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("source pruning=%d %v", n, err)
	}
	publishBackfillDeliveryEvent(t, f.store, f.account, "reused")
	items, err := f.store.ListEventReplayBackfillItems(ctx, f.account, f.job.ID, api.EventReplayBackfillItemsQuery{})
	if err != nil || len(items.Items) != 1 || items.Items[0].ReceiptURL != "" || items.Items[0].AttemptHistoryURL != "" {
		t.Fatalf("links to reused identity=%+v %v", items, err)
	}
}
