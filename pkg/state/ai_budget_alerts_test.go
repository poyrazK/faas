package state_test

// adr: 20261027132936 — amendment 2: ai_budget_alerts counts the budget
// threshold crossings of the integrations bound to an app within the window,
// leaving out other apps' own budgets and unbound integrations.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAIBudgetAlertsSince(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acct, bound := callStatsIntegration(t, s)
	unbound, err := s.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: "other", Origin: "https://api.anthropic.com",
		AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := func(slug string) state.App {
		t.Helper()
		created, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	shop, other := app("shop"), app("other")
	for _, a := range []state.App{shop, other} {
		if _, err := s.BindOutboundIntegration(ctx, acct.ID, a.ID, bound); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	crossing := func(integrationID, budget string, appID any, endUser, tag any, percent int, crossed time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbound_ai_budget_alerts
			    (integration_id, account_id, budget, app_id, end_user, period_start, threshold_percent, limit_value, used_value, tag, crossed_at)
			VALUES ($1, $2, $3, $4, $5, $6::date, $7, 2500000, 2000000, $8, $9)`,
			integrationID, acct.ID, budget, appID, endUser, crossed, percent, tag, crossed); err != nil {
			t.Fatal(err)
		}
	}
	crossing(bound, "daily_cost", nil, nil, nil, 80, now.Add(-5*time.Minute))
	crossing(bound, "user_daily_cost", nil, "user-1", nil, 100, now.Add(-10*time.Minute))
	crossing(bound, "app_daily_tokens", other.ID, nil, nil, 80, now.Add(-5*time.Minute)) // another app's own budget
	crossing(unbound.ID, "daily_cost", nil, nil, nil, 100, now.Add(-5*time.Minute))      // not bound to shop
	crossing(bound, "monthly_cost", nil, nil, nil, 80, now.Add(-2*time.Hour))            // before the window

	got, total, err := s.AIBudgetAlertsSince(ctx, acct.ID, shop.ID, now.Add(-time.Hour), api.AIBudgetAlertNotes)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("crossings = %+v total %d err %v; want the two of shop's bound integration in the window", got, total, err)
	}
	if got[0].Budget != "daily_cost" || got[0].IntegrationName != "llm" || got[0].ThresholdPercent != 80 || got[0].Limit != 2_500_000 ||
		got[0].Used != 2_000_000 || got[1].EndUser != "user-1" {
		t.Fatalf("crossings = %+v; want newest first with integration names", got)
	}
	if _, total, err := s.AIBudgetAlertsSince(ctx, acct.ID, other.ID, now.Add(-time.Hour), 1); err != nil || total != 3 {
		t.Fatalf("other app: total %d err %v; want its own budget too", total, err)
	}
	if _, total, err := s.AIBudgetAlertsSince(ctx, uuid.NewString(), shop.ID, now.Add(-time.Hour), 1); err != nil || total != 0 {
		t.Fatalf("another account: total %d err %v", total, err)
	}
}
