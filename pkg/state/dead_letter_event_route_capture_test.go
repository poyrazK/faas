package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventRouteCaptureStore is what the prune-time capture contract uses.
type eventRouteCaptureStore interface {
	UpsertEventSubscription(ctx context.Context, accountID, appID, source, typ string, filter json.RawMessage) (state.EventSubscription, bool, error)
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
	ClaimDuePublishedEvent(ctx context.Context, now time.Time) (*state.PublishedEventWork, error)
	RecordPublishedEventRecipientProgress(ctx context.Context, id int64, token, recipientID string, progress state.PublishedEventRecipientProgress) error
	FinishPublishedEvent(ctx context.Context, id int64, token string, routeErr error) error
	PruneSettledPublishedEvents(ctx context.Context, now time.Time, limit int) (int64, error)
	ListDeadLetterEvents(ctx context.Context, appID string, limit int, before string) ([]state.DeadLetterEvent, error)
	ReplayDeadLetterEvent(ctx context.Context, accountID, appID, eventID string) (state.DeadLetterEvent, error)
}

// Pruning a receipt moves each failed recipient into the dead-letter ledger
// as an event_route entry (ADR-20261026163443); a receipt without failures,
// or a recipient that routed, leaves nothing.
func TestMemEventRouteDeadLetterCaptureOnPrune(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "route-capture@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := state.CanonicalMemUUID(account.ID)
	app, err := m.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "route-capture"})
	if err != nil {
		t.Fatal(err)
	}
	checkEventRouteCapture(t, ctx, m, accountID, app.ID)
}

func TestPgEventRouteDeadLetterCaptureOnPrune(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "route-capture")
	checkEventRouteCapture(t, ctx, s, accountID, appID)
}

func checkEventRouteCapture(t *testing.T, ctx context.Context, s eventRouteCaptureStore, accountID, appID string) {
	t.Helper()
	failed, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	routed, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	failedAt := time.Now().UTC().Truncate(time.Microsecond)
	publish := func(id string, fail bool) {
		t.Helper()
		if err := s.AppendEvent(ctx, "apid", "event.published", &accountID,
			[]byte(`{"id":"`+id+`","source":"orders","type":"order.created","data":{"amount":7}}`)); err != nil {
			t.Fatal(err)
		}
		work, err := s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		outcome := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: 1, UpdatedAt: failedAt}
		if fail {
			outcome = state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 12,
				FailureCode: state.EventFanoutFailureCodeTargetUnavailable, LastError: "target app is unavailable",
				RetryStopReason: "non_retryable", UpdatedAt: failedAt}
		}
		if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, failed.ID, outcome); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, routed.ID,
			state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: 1, UpdatedAt: failedAt}); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
	}
	publish("evt-route-lost", true)
	publish("evt-route-ok", false)

	if n, err := s.PruneSettledPublishedEvents(ctx, time.Now().UTC().Add(30*24*time.Hour), 100); err != nil || n != 2 {
		t.Fatalf("prune = %d, %v; want both receipts", n, err)
	}
	events, err := s.ListDeadLetterEvents(ctx, appID, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("dead letters after prune = %+v; want one event_route entry", events)
	}
	ev := events[0]
	var payload, detail map[string]any
	if json.Unmarshal(ev.Payload, &payload) != nil || json.Unmarshal(ev.ErrorDetail, &detail) != nil {
		t.Fatalf("entry JSON = %s / %s", ev.Payload, ev.ErrorDetail)
	}
	if ev.Source != state.DeadLetterSourceEventRoute || ev.Origin != "event_subscription" ||
		ev.ErrorKind != state.EventFanoutFailureCodeTargetUnavailable || ev.RetryCount != 12 ||
		!ev.LastFailedAt.Equal(failedAt) || ev.ReplayedAt != nil || payload["id"] != "evt-route-lost" ||
		detail["subscription_id"] != failed.ID || detail["event_id"] != "evt-route-lost" || detail["event_source"] != "orders" ||
		detail["event_type"] != "order.created" || detail["last_error"] != "target app is unavailable" ||
		detail["retry_stop_reason"] != "non_retryable" || detail["retryable"] != false {
		t.Fatalf("event_route entry = %+v payload=%v detail=%v", ev, payload, detail)
	}
	if _, err := s.ReplayDeadLetterEvent(ctx, ev.AccountID, appID, ev.ID); !errors.Is(err, state.ErrDeadLetterNotReplayable) {
		t.Fatalf("replay captured entry = %v, want ErrDeadLetterNotReplayable", err)
	}
}

// The capture keeps an entry whose app is gone under the account only, and
// writes nothing for an account pending deletion.
func TestPgEventRouteDeadLetterCaptureOwners(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "route-owners")
	sub, _, err := s.UpsertEventSubscription(ctx, accountID, appID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	failReceipt := func(id string) int64 {
		t.Helper()
		if err := s.AppendEvent(ctx, "apid", "event.published", &accountID, []byte(`{"id":"`+id+`","source":"orders","type":"order.created","data":{}}`)); err != nil {
			t.Fatal(err)
		}
		work, err := s.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, sub.ID, state.PublishedEventRecipientProgress{
			State: state.PublishedEventRecipientFailed, Attempts: 3, FailureCode: state.EventFanoutFailureCodeInvalidSubscription,
			LastError: "subscription is invalid", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		return work.ID
	}
	// A recipient whose app is no longer the account's keeps the entry at
	// account scope.
	orphan := failReceipt("evt-orphan")
	if _, err := pool.Exec(ctx, `UPDATE event_fanout_outbox SET recipient_snapshot =
		jsonb_set(recipient_snapshot, '{0,app_id}', to_jsonb($2::text)) WHERE id = $1`, orphan, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSettledPublishedEvents(ctx, time.Now().UTC().Add(30*24*time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("prune orphan = %d, %v", n, err)
	}
	events, err := s.ListDeadLetterEventsForAccount(ctx, accountID, 20, "")
	if err != nil || len(events) != 1 || events[0].AppID != "" || events[0].ErrorKind != state.EventFanoutFailureCodeInvalidSubscription {
		t.Fatalf("account entries after orphan prune = %+v, %v", events, err)
	}

	failReceipt("evt-deleting")
	if _, err := pool.Exec(ctx, `UPDATE accounts SET status = 'deleted_pending' WHERE id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSettledPublishedEvents(ctx, time.Now().UTC().Add(30*24*time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("prune pending-deletion receipt = %d, %v", n, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dead_letter_events WHERE account_id = $1 AND source = 'event_route'`, accountID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event_route rows for a pending-deletion account = %d, %v; want only the earlier one", count, err)
	}
}
