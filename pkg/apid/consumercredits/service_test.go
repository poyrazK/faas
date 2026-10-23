package consumercredits_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/consumercredits"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// quoteHost prices every consumer and tenant month at amount and counts
// the quotes.
type quoteHost struct {
	store                        *state.MemStore
	amount                       int64
	consumerQuotes, tenantQuotes int
}

func (h *quoteHost) Store() state.Store { return h.store }

func (h *quoteHost) ConsumerRoute(http.ResponseWriter, *http.Request, state.Account) (state.App, state.APIConsumer, bool) {
	return state.App{}, state.APIConsumer{}, false
}

func (h *quoteHost) QuoteConsumerUsage(context.Context, string, string, string, time.Time, time.Time) (billing.APIConsumerUsageQuote, error) {
	h.consumerQuotes++
	return billing.APIConsumerUsageQuote{Currency: "EUR", AmountMillicents: h.amount}, nil
}

func (h *quoteHost) TenantRoute(http.ResponseWriter, *http.Request, state.Account) (state.PlatformTenant, bool) {
	return state.PlatformTenant{}, false
}

func (h *quoteHost) QuoteTenantMonthUsage(context.Context, string, string, time.Time) (string, int64, error) {
	h.tenantQuotes++
	return "EUR", h.amount, nil
}

func (h *quoteHost) Audit(context.Context, string, string, map[string]any) {}

// TestSpendCheckReusesUnchangedMonth: the worker reprices a month only when
// usage was recorded since its last check, or the last price is older than
// ConsumerSpendCapRepriceInterval; a reused price still caps
// (ADR-20261020211500).
//
// adr: 20261020211500
func TestSpendCheckReusesUnchangedMonth(t *testing.T) {
	start := time.Now().UTC()
	if billing.MonthStart(start) != billing.MonthStart(start.Add(time.Hour)) {
		t.Skip("a check across the month boundary always reprices")
	}
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "spend-reuse@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "spend-reuse", Type: state.AppTypeApp, RAMMB: 256,
		MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, "reuse", "Reuse customer")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "reuse-tenant", "Reuse tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutAPIConsumerSpendCap(ctx, state.APIConsumerSpendCap{ConsumerID: consumer.ID, AccountID: acct.ID, AppID: app.ID,
		Currency: "EUR", MonthlyCapMillicents: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutPlatformTenantSpendCap(ctx, state.PlatformTenantSpendCap{TenantID: tenant.ID, AccountID: acct.ID,
		Currency: "EUR", MonthlyCapMillicents: 100}); err != nil {
		t.Fatal(err)
	}
	host := &quoteHost{store: store, amount: 500}
	now := time.Now().UTC().Add(time.Second)
	svc := &consumercredits.Service{Host: host, Now: func() time.Time { return now }}

	for _, step := range []struct {
		name    string
		advance time.Duration
		usage   bool
		quotes  int
	}{
		{"first check prices", 0, false, 1},
		{"no usage since reuses", 2 * time.Minute, false, 1},
		{"usage since reprices", 2 * time.Minute, true, 2},
		{"old price reprices", api.ConsumerSpendCapRepriceInterval + time.Minute, false, 3},
	} {
		now = now.Add(step.advance)
		if step.usage {
			if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: acct.ID,
				AppID: app.ID, ConsumerKey: consumer.ID, PlatformTenantID: tenant.ID, WindowStart: now.Truncate(time.Minute),
				RequestCount: 1, BillableUnits: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.CheckOnce(ctx); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if host.consumerQuotes != step.quotes || host.tenantQuotes != step.quotes {
			t.Fatalf("%s: quotes = %d consumer, %d tenant; want %d", step.name, host.consumerQuotes, host.tenantQuotes, step.quotes)
		}
		c, err := store.GetAPIConsumerSpendCap(ctx, acct.ID, consumer.ID)
		if err != nil || !c.Capped || c.AmountDueMillicents == nil || *c.AmountDueMillicents != 500 {
			t.Fatalf("%s: consumer cap = %+v, %v", step.name, c, err)
		}
		tc, err := store.GetPlatformTenantSpendCap(ctx, acct.ID, tenant.ID)
		if err != nil || !tc.Capped || tc.AmountDueMillicents == nil || *tc.AmountDueMillicents != 500 {
			t.Fatalf("%s: tenant cap = %+v, %v", step.name, tc, err)
		}
	}
}
