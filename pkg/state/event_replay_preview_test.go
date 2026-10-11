package state_test

// adr: 624

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type replayPreviewTestStore interface {
	state.Store
	state.EventSubscriptionStore
	state.EventReplayPreviewStore
	state.PublishedEventWorkStore
	state.PublishedEventRetentionStore
	state.EventReceiptStore
}

func forReplayPreviewStores(t *testing.T, run func(*testing.T, replayPreviewTestStore, *pgxpool.Pool)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, state.NewMemStore(), nil) })
	t.Run("postgres", func(t *testing.T) {
		s, pool, _ := pgStoreWithPool(t)
		run(t, s, pool)
	})
}

func seedReplayPreviewApp(t *testing.T, s replayPreviewTestStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@preview.example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.MustParse(a.ID).String()
	app, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "preview-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	return accountID, uuid.MustParse(app.ID).String()
}

func publishReplayPreviewEvent(t *testing.T, s replayPreviewTestStore, accountID, id, source string, amount int) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"id": id, "source": source, "type": "invoice.paid", "time": "1990-01-01T00:00:00Z", "data": map[string]any{"amount": amount, "private": "payload-must-stay-private"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(context.Background(), "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
}

func TestEventReplayPreviewHistoricalMatchingCutoffAndRetention(t *testing.T) {
	forReplayPreviewStores(t, func(t *testing.T, s replayPreviewTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, app := seedReplayPreviewApp(t, s)
		sibling, _, err := s.UpsertEventSubscription(ctx, account, app, "*", "*", nil)
		if err != nil {
			t.Fatal(err)
		}
		from := time.Now().UTC().Add(-time.Minute)
		publishReplayPreviewEvent(t, s, account, "before-target", "billing.us", 150)
		old, err := s.ClaimDuePublishedEvent(ctx, time.Now())
		if err != nil || len(old.RecipientSnapshot) != 1 || old.RecipientSnapshot[0].ID != sibling.ID {
			t.Fatalf("original=%+v %v", old, err)
		}
		target, _, err := s.UpsertEventSubscription(ctx, account, app, "billing.*", "invoice.paid", json.RawMessage(`{"data":{"amount":{"$gt":100}}}`))
		if err != nil {
			t.Fatal(err)
		}
		publishReplayPreviewEvent(t, s, account, "captured", "billing.us", 200)
		publishReplayPreviewEvent(t, s, account, "filtered", "billing.us", 10)
		publishReplayPreviewEvent(t, s, account, "unrelated", "shipping.us", 300)
		before, err := s.EventReceipt(ctx, account, "billing.us", "before-target", state.EventReceiptCursor{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		q := state.EventReplayPreviewQuery{AppID: app, SubscriptionID: target.ID, EventReplayPreviewOptions: api.EventReplayPreviewOptions{From: from, Until: time.Now().Add(time.Hour), Limit: 1}}
		first, err := s.PreviewEventReplay(ctx, account, q)
		if err != nil || first.ScannedCount != 1 || first.MatchedCount != 1 || first.NextAfter == "" || first.Matches[0].EventID != "before-target" || first.Matches[0].OriginalRecipient != "not_captured" || first.Retention.HistoryComplete || first.Retention.EarliestRetainedAt == nil || first.Retention.SettledRetentionSeconds != 30*24*60*60 {
			t.Fatalf("first=%+v %v", first, err)
		}
		after, err := s.EventReceipt(ctx, account, "billing.us", "before-target", state.EventReceiptCursor{}, 100)
		if err != nil || len(after.Recipients) != 1 || after.Recipients[0].SubscriptionID != sibling.ID || after.Recipients[0].Routing.State != before.Recipients[0].Routing.State {
			t.Fatalf("preview changed original receipt: before=%+v after=%+v err=%v", before, after, err)
		}
		invocations, err := s.ListInvocationsForAccount(ctx, account, 100, "")
		if err != nil || len(invocations) != 0 {
			t.Fatalf("preview created invocations: %+v %v", invocations, err)
		}
		// Remove the cursor row; keyset continuation must still work. No sibling
		// routing outcome is written by preview itself.
		if err := s.FinishPublishedEvent(ctx, old.ID, old.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := s.PruneDeliveredPublishedEvents(ctx, time.Now().Add(time.Hour), 1); err != nil || n != 1 {
			t.Fatalf("prune=%d %v", n, err)
		}
		publishReplayPreviewEvent(t, s, account, "late", "billing.us", 300)
		// Reuse a pruned identity: its new acceptance also lies after the cutoff.
		publishReplayPreviewEvent(t, s, account, "before-target", "billing.us", 150)
		q.After = first.NextAfter
		counts := []int{}
		for page := 0; page < 3; page++ {
			r, err := s.PreviewEventReplay(ctx, account, q)
			if err != nil || r.ScannedCount != 1 || !r.CutoffAt.Equal(first.CutoffAt) || r.SubscriptionRevision != first.SubscriptionRevision {
				t.Fatalf("page %d=%+v %v", page, r, err)
			}
			counts = append(counts, r.MatchedCount)
			if page == 0 && (r.AlreadyCapturedCount != 1 || r.Matches[0].EventID != "captured" || r.Matches[0].OriginalRecipient != "captured") {
				t.Fatalf("captured=%+v", r)
			}
			if page == 1 && (r.FilterMismatchCount != 1 || r.NextAfter == "") {
				t.Fatalf("empty filtered page must continue: %+v", r)
			}
			if page == 2 && (r.PatternMismatchCount != 1 || r.NextAfter != "") {
				t.Fatalf("final=%+v", r)
			}
			q.After = r.NextAfter
		}
		if !reflect.DeepEqual(counts, []int{1, 0, 0}) {
			t.Fatalf("matches=%v", counts)
		}
	})
}

func TestEventReplayPreviewScopeAndCancellation(t *testing.T) {
	forReplayPreviewStores(t, func(t *testing.T, s replayPreviewTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, app := seedReplayPreviewApp(t, s)
		target, _, err := s.UpsertEventSubscription(ctx, account, app, "*", "*", nil)
		if err != nil {
			t.Fatal(err)
		}
		q := state.EventReplayPreviewQuery{AppID: app, SubscriptionID: target.ID, EventReplayPreviewOptions: api.EventReplayPreviewOptions{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), Limit: 1}}
		for _, foreign := range []state.EventReplayPreviewQuery{{AppID: uuid.NewString(), SubscriptionID: target.ID, EventReplayPreviewOptions: q.EventReplayPreviewOptions}, {AppID: app, SubscriptionID: uuid.NewString(), EventReplayPreviewOptions: q.EventReplayPreviewOptions}} {
			if _, err := s.PreviewEventReplay(ctx, account, foreign); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign target=%v", err)
			}
		}
		foreignAccount, _ := seedReplayPreviewApp(t, s)
		if _, err := s.PreviewEventReplay(ctx, foreignAccount, q); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign account=%v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.PreviewEventReplay(cancelled, account, q); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled=%v", err)
		}
		q.Limit = api.EventReplayPreviewPageMax + 1
		if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrEventReplayPreviewQuery) {
			t.Fatalf("unbounded=%v", err)
		}
	})
}

func TestPgEventReplayPreviewTargetMutationAndLegacy(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := context.Background()
	account, app := seedReplayPreviewApp(t, s)
	sub, _, err := s.UpsertEventSubscription(ctx, account, app, "*", "*", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy-1", "legacy-2", "legacy-3"} {
		publishReplayPreviewEvent(t, s, account, id, "orders", 150)
	}
	accepted := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET created_at=$1,recipient_snapshot=NULL WHERE account_id=$2", accepted, account); err != nil {
		t.Fatal(err)
	}
	q := state.EventReplayPreviewQuery{AppID: app, SubscriptionID: sub.ID, EventReplayPreviewOptions: api.EventReplayPreviewOptions{From: accepted, Until: accepted.Add(2 * time.Hour), Limit: 1}}
	first, err := s.PreviewEventReplay(ctx, account, q)
	if err != nil || first.MatchedCount != 1 || first.Matches[0].OriginalRecipient != "unknown" || first.NextAfter == "" {
		t.Fatalf("legacy=%+v %v", first, err)
	}
	q.After = first.NextAfter
	second, err := s.PreviewEventReplay(ctx, account, q)
	if err != nil || second.MatchedCount != 1 || second.Matches[0].EventID == first.Matches[0].EventID {
		t.Fatalf("tied tuple=%+v %v", second, err)
	}
	boundary := q
	boundary.After = ""
	boundary.From = accepted.Add(time.Nanosecond)
	if r, err := s.PreviewEventReplay(ctx, account, boundary); err != nil || r.ScannedCount != 0 {
		t.Fatalf("nanosecond lower boundary=%+v %v", r, err)
	}
	boundary.From = accepted
	boundary.Until = accepted.Add(time.Nanosecond)
	if r, err := s.PreviewEventReplay(ctx, account, boundary); err != nil || r.ScannedCount != 1 || r.NextAfter == "" {
		t.Fatalf("nanosecond upper boundary=%+v %v", r, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE event_subscriptions SET filter=$1 WHERE id=$2", []byte(`{"data":{"amount":1}}`), sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrEventReplayPreviewChanged) {
		t.Fatalf("changed filter=%v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE event_subscriptions SET enabled=false WHERE id=$1", sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrEventReplayPreviewDisabled) {
		t.Fatalf("disabled=%v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE event_subscriptions SET enabled=true,filter='{}',updated_at=updated_at+interval '1 second' WHERE id=$1", sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrEventReplayPreviewChanged) {
		t.Fatalf("re-enabled=%v", err)
	}
	if _, err := s.UpsertAppWorkPolicy(ctx, account, app, workpolicy.Policy{Name: "keyed-preview", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEventWorkBinding(ctx, app, sub.ID, "keyed-preview", "data.amount"); err != nil {
		t.Fatal(err)
	}
	q.After = ""
	if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrEventReplayPreviewUnsupported) {
		t.Fatalf("work-bound=%v", err)
	}
	foreign, err := s.CreateAccount(ctx, uuid.NewString()+"@transfer-preview.example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE apps SET account_id=$1 WHERE id=$2", foreign.ID, app); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewEventReplay(ctx, account, q); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("transferred target=%v", err)
	}
}
