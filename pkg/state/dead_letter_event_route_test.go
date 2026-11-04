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

// eventRouteDeadLetterStore is what the event_route ledger contract uses.
type eventRouteDeadLetterStore interface {
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	ClaimInvocation(ctx context.Context, id, instanceID string, leaseSeconds int) (state.Invocation, error)
	FailInvocation(ctx context.Context, id, lastError string, retryAfter time.Duration, budget int, opts ...state.FailOption) error
	ListDeadLetterEvents(ctx context.Context, appID string, limit int, before string) ([]state.DeadLetterEvent, error)
	ListDeadLetterEventsForAccount(ctx context.Context, accountID string, limit int, before string) ([]state.DeadLetterEvent, error)
	ReplayDeadLetterEvent(ctx context.Context, accountID, appID, eventID string) (state.DeadLetterEvent, error)
	ReplayDeadLetterEventForAccount(ctx context.Context, accountID, eventID string) (state.DeadLetterEvent, error)
	ReplayDeadLetterEvents(ctx context.Context, accountID, appID string, limit int) (int, error)
	ReplayDeadLetterEventsForAccount(ctx context.Context, accountID string, limit int) (int, error)
	DeleteDeadLetterEvent(ctx context.Context, accountID, appID, eventID string) error
	DeleteDeadLetterEventForAccount(ctx context.Context, accountID, eventID string) error
}

// seedEventRoute writes one event_route ledger row; appID may be empty.
type seedEventRoute func(t *testing.T, accountID, appID string, failedAt time.Time) string

// An event_route entry (ADR-20261026163443) is listed and can be discarded,
// but replay refuses it and replay-all passes it by, even when it is the
// newest open entry and the page holds one.
func TestMemEventRouteDeadLetterCannotBeReplayed(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	account, app := eventRouteFixture(t, ctx, m.CreateAccount, m.CreateApp)
	seed := func(t *testing.T, accountID, appID string, failedAt time.Time) string {
		id := uuid.NewString()
		m.SeedDeadLetterEventForTest(state.DeadLetterEvent{
			ID: id, AccountID: accountID, AppID: appID, Source: state.DeadLetterSourceEventRoute,
			SourceID: uuid.NewString(), Origin: "event_subscription", Payload: json.RawMessage(`{"id":"evt-1","source":"orders","type":"order.created","data":{}}`),
			Headers: json.RawMessage(`{}`), ErrorKind: "target_unavailable", ErrorDetail: json.RawMessage(`{"subscription_id":"sub"}`),
			RetryCount: 12, FirstFailedAt: failedAt, LastFailedAt: failedAt, CreatedAt: failedAt,
		})
		return id
	}
	checkEventRouteDeadLetter(t, ctx, m, account.ID, app.ID, seed)
}

func TestPgEventRouteDeadLetterCannotBeReplayed(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := eventRouteFixture(t, ctx, s.CreateAccount, s.CreateApp)
	seed := func(t *testing.T, accountID, appID string, failedAt time.Time) string {
		t.Helper()
		var app any
		if appID != "" {
			app = appID
		}
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO dead_letter_events (account_id, app_id, source, source_id, origin, event_payload,
				headers, error_kind, error_detail, retry_count, first_failed_at, last_failed_at)
			VALUES ($1, $2, 'event_route', $3, 'event_subscription', '{"id":"evt-1","source":"orders","type":"order.created","data":{}}',
				'{}', 'target_unavailable', '{"subscription_id":"sub"}', 12, $4, $4)
			RETURNING id::text`, accountID, app, uuid.NewString(), failedAt).Scan(&id); err != nil {
			t.Fatalf("seed event_route row: %v", err)
		}
		return id
	}
	checkEventRouteDeadLetter(t, ctx, s, account.ID, app.ID, seed)
}

func eventRouteFixture(t *testing.T, ctx context.Context,
	createAccount func(context.Context, string, api.Plan) (state.Account, error),
	createApp func(context.Context, state.App) (state.App, error),
) (state.Account, state.App) {
	t.Helper()
	account, err := createAccount(ctx, "event-route-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := createApp(ctx, state.App{
		AccountID: account.ID, Slug: "event-route-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func checkEventRouteDeadLetter(t *testing.T, ctx context.Context, s eventRouteDeadLetterStore, accountID, appID string, seed seedEventRoute) {
	t.Helper()
	now := time.Now().UTC()
	deadInvocation := func() {
		t.Helper()
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: accountID, Source: state.InvocationQueue, State: state.InvocationPending,
			Method: "POST", Path: "/", Payload: []byte(`{}`), Headers: []byte(`{}`), DueAt: now, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimInvocation(ctx, inv.ID, "event-route-dlq", 30); err != nil {
			t.Fatal(err)
		}
		if err := s.FailInvocation(ctx, inv.ID, "handler failed", time.Second, 1); err != nil {
			t.Fatal(err)
		}
	}
	deadInvocation()
	deadInvocation()
	// Newer than the invocations, so an unfiltered replay-all page of one
	// would pick it first.
	appRoute := seed(t, accountID, appID, now.Add(time.Minute))
	accountRoute := seed(t, accountID, "", now.Add(2*time.Minute))

	listed := func(events []state.DeadLetterEvent, err error, id string) bool {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			if sameDeadLetterID(ev.ID, id) {
				if ev.Source != state.DeadLetterSourceEventRoute || ev.ErrorKind != "target_unavailable" || ev.RetryCount != 12 || ev.ReplayedAt != nil {
					t.Fatalf("event_route row = %+v", ev)
				}
				return true
			}
		}
		return false
	}
	if events, err := s.ListDeadLetterEvents(ctx, appID, 20, ""); !listed(events, err, appRoute) {
		t.Fatalf("app list misses the event_route row: %+v", events)
	}
	if events, err := s.ListDeadLetterEventsForAccount(ctx, accountID, 20, ""); !listed(events, err, appRoute) || !listed(events, err, accountRoute) {
		t.Fatalf("account list misses an event_route row: %+v", events)
	}

	for name, replay := range map[string]func() error{
		"app":         func() error { _, err := s.ReplayDeadLetterEvent(ctx, accountID, appID, appRoute); return err },
		"account":     func() error { _, err := s.ReplayDeadLetterEventForAccount(ctx, accountID, appRoute); return err },
		"account-app": func() error { _, err := s.ReplayDeadLetterEventForAccount(ctx, accountID, accountRoute); return err },
	} {
		if err := replay(); !errors.Is(err, state.ErrDeadLetterNotReplayable) || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s replay of event_route = %v, want ErrDeadLetterNotReplayable", name, err)
		}
	}
	if n, err := s.ReplayDeadLetterEvents(ctx, accountID, appID, 1); err != nil || n != 1 {
		t.Fatalf("app replay-all = %d, %v; want the invocation replayed past the event_route row", n, err)
	}
	if n, err := s.ReplayDeadLetterEventsForAccount(ctx, accountID, 1); err != nil || n != 1 {
		t.Fatalf("account replay-all = %d, %v; want the second invocation replayed", n, err)
	}
	if n, err := s.ReplayDeadLetterEventsForAccount(ctx, accountID, 20); err != nil || n != 0 {
		t.Fatalf("account replay-all with only event_route rows open = %d, %v", n, err)
	}

	if err := s.DeleteDeadLetterEvent(ctx, accountID, appID, appRoute); err != nil {
		t.Fatalf("discard app event_route row: %v", err)
	}
	if err := s.DeleteDeadLetterEventForAccount(ctx, accountID, accountRoute); err != nil {
		t.Fatalf("discard account event_route row: %v", err)
	}
	events, err := s.ListDeadLetterEventsForAccount(ctx, accountID, 20, "")
	if listed(events, err, appRoute) || listed(events, err, accountRoute) {
		t.Fatalf("discarded event_route rows still listed: %+v", events)
	}
}

func sameDeadLetterID(a, b string) bool {
	x, errA := uuid.Parse(a)
	y, errB := uuid.Parse(b)
	return errA == nil && errB == nil && x == y
}
