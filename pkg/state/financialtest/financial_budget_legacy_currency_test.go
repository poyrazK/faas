package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1430 — a draft written before the USD switch keeps EUR. Every policy
// read of its account must still decode it: one undecodable row refused every
// app of the account at the gateway and the scheduler. The draft can be moved
// to USD or deleted, and it can never be enabled in EUR.
func TestFinancialBudgetLegacyCurrencyDraftStaysReadable(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a := financialAccount(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "legacy-draft", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	legacy := `{"name":"before usd","scope":{"kind":"account"},"currency":"EUR","meters":["compute"],"basis":"net_usage",` +
		`"limit_millicents":1000,"notify_millicents":[500],"mode":"monitored","action":"notify","drain_seconds":0,"resume_rule":"manual","enabled":false}`
	if _, err := pool.Exec(ctx, `INSERT INTO financial_budget_policies(id, account_id, revision, spec) VALUES($1, $2, 1, $3::jsonb)`, id, a.ID, legacy); err != nil {
		t.Fatalf("seed legacy draft: %v", err)
	}

	rows, err := store.ListFinancialBudgets(ctx, a.ID)
	if err != nil || len(rows) != 1 || rows[0].Spec.Currency != financial.LegacyBillingCurrency || rows[0].Spec.Enabled {
		t.Fatalf("list with a legacy draft = %+v, %v", rows, err)
	}
	if policies, err := store.FinancialComputePolicies(ctx, a.ID, app.ID); err != nil || len(policies) != 1 {
		t.Fatalf("compute policies with a legacy draft = %+v, %v", policies, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, app, "", time.Now().UTC()); err != nil {
		t.Fatalf("admission with a legacy draft: %v", err)
	}

	enable := rows[0].Spec
	enable.Enabled = true
	if _, err := store.UpdateFinancialBudget(ctx, a.ID, id, 1, "account:"+a.ID, enable); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("enabling in EUR: %v, want ErrInvalidArgument", err)
	}
	usd := enable
	usd.Currency = financial.BillingCurrency
	updated, err := store.UpdateFinancialBudget(ctx, a.ID, id, 1, "account:"+a.ID, usd)
	if err != nil || updated.Revision != 2 || updated.Spec.Currency != financial.BillingCurrency {
		t.Fatalf("update to USD = %+v, %v", updated, err)
	}
	if _, err := store.DeleteFinancialBudget(ctx, a.ID, id, 2, "account:"+a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
