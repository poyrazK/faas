package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventfilter"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventFilterCapCase struct {
	name   string
	filter json.RawMessage
	bound  eventfilter.Bound
}

func eventFilterCapCases() []eventFilterCapCase {
	depth := api.EventSubscriptionFilterMaxDepth
	return []eventFilterCapCase{
		{name: "typical", filter: json.RawMessage(`{"data":{"amount":{"$gt":100}}}`)},
		{name: "too large", bound: eventfilter.BoundBytes,
			filter: json.RawMessage(`{"data":{"id":{"$eq":"` + strings.Repeat("x", api.EventSubscriptionFilterMaxBytes) + `"}}}`)},
		{name: "too deep", bound: eventfilter.BoundDepth,
			filter: json.RawMessage(strings.Repeat(`{"a":`, depth) + `{"$eq":1}` + strings.Repeat("}", depth))},
	}
}

func checkEventFilterCap(t *testing.T, tc eventFilterCapCase, err error) {
	t.Helper()
	var bound *eventfilter.BoundError
	if tc.bound == "" {
		if err != nil {
			t.Fatalf("UpsertEventSubscription = %v, want nil", err)
		}
		return
	}
	if !errors.As(err, &bound) || bound.Bound != tc.bound {
		t.Fatalf("UpsertEventSubscription = %v, want a %s bound error", err, tc.bound)
	}
}

// Events hunt #9: the store refuses a subscription filter over the size or
// depth cap even when a caller skipped manifest validation.
func TestMemStoreEventSubscriptionFilterCaps(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "event-filter-caps@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := state.CanonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-filter-caps"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range eventFilterCapCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "billing.*", "invoice.paid", tc.filter)
			checkEventFilterCap(t, tc, err)
		})
	}
}

func TestPgEventSubscriptionFilterCaps(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "event-filter-caps-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "event-filter-caps"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range eventFilterCapCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "billing.*", "invoice.paid", tc.filter)
			checkEventFilterCap(t, tc, err)
		})
	}

	// The database backstop refuses a write that bypasses the store.
	oversized := `{"k":"` + strings.Repeat("x", api.EventSubscriptionFilterStoredMaxBytes) + `"}`
	_, err = pool.Exec(ctx, `insert into event_subscriptions (account_id, app_id, source, type, filter)
		values ($1, $2, 'billing.*', 'invoice.void', $3::jsonb)`, account.ID, app.ID, oversized)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "event_subscriptions_filter_size_chk" {
		t.Fatalf("raw oversized insert = %v, want event_subscriptions_filter_size_chk violation", err)
	}
}
