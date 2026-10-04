package financialtest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 530 — retained corrections cannot rewrite, over-credit, or reattribute usage.
func financialAdjustmentSuite(t *testing.T, store financialTestStore) {
	a := financialAccount(t, store)
	other := financialAccount(t, store)
	start, _ := financialPeriod()
	start = start.AddDate(0, -1, 0)
	end := start.AddDate(0, 1, 0)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "deleted-original", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	price := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: end, Plan: api.PlanHobby, EffectiveFrom: start, DeliveryMode: "live", Price: financial.Price{Version: "old-terms", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), price); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(t.Context(), a.ID, app.ID, uuid.NewString(), start.Add(time.Hour), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	head, err := store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListFinancialUsageEvidence(t.Context(), a.ID, start, end, 0, head, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("original: %+v,%v", rows, err)
	}
	original := rows[0]
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	request := state.FinancialAdjustment{AccountID: a.ID, SourceID: "adjustment:first", CorrectsSourceID: original.Evidence.SourceID, Quantity: -60, Actor: "billing-operator", Reason: "duplicate residency interval"}
	type result struct {
		row state.FinancialUsageRecord
		err error
	}
	results := make(chan result, 2)
	for _, source := range []string{"adjustment:first", "adjustment:second"} {
		go func() {
			r := request
			r.SourceID = source
			row, err := store.AppendFinancialAdjustment(t.Context(), r)
			results <- result{row, err}
		}()
	}
	var corrected state.FinancialUsageRecord
	var rejected int
	for range 2 {
		r := <-results
		if r.err == nil {
			corrected = r.row
		} else if errors.Is(r.err, state.ErrInvalidArgument) {
			rejected++
		} else {
			t.Fatal(r.err)
		}
	}
	if corrected.Sequence == 0 || rejected != 1 || corrected.PriceVersion != original.PriceVersion || corrected.Evidence.Attribution != original.Evidence.Attribution || corrected.Plan != original.Plan || !corrected.Evidence.Start.Equal(original.Evidence.Start) {
		t.Fatalf("concurrent correction lineage: %+v,rejected=%d", corrected, rejected)
	}
	request.SourceID = corrected.Evidence.SourceID
	replay, err := store.AppendFinancialAdjustment(t.Context(), request)
	if err != nil || !reflect.DeepEqual(replay, corrected) {
		t.Fatalf("correction replay: %+v,%v", replay, err)
	}
	request.Quantity = -59
	if _, err := store.AppendFinancialAdjustment(t.Context(), request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	request.Quantity = -1
	request.AccountID = other.ID
	if _, err := store.AppendFinancialAdjustment(t.Context(), request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign correction: %v", err)
	}
	request.AccountID = a.ID
	request.SourceID = "adjustment:chained"
	request.CorrectsSourceID = corrected.Evidence.SourceID
	if _, err := store.AppendFinancialAdjustment(t.Context(), request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("chained correction: %v", err)
	}
	request.CorrectsSourceID = original.Evidence.SourceID
	request.SourceID = "adjustment:remaining"
	request.Quantity = -40
	if _, err := store.AppendFinancialAdjustment(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.SourceID = "adjustment:excess"
	request.Quantity = -1
	if _, err := store.AppendFinancialAdjustment(t.Context(), request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("correction exceeded original: %v", err)
	}
	frozen, err := store.ListFinancialUsageEvidence(t.Context(), a.ID, start, end, 0, head, 10)
	if err != nil || !reflect.DeepEqual(frozen, rows) {
		t.Fatalf("old head changed: %+v,%v", frozen, err)
	}
	head, err = store.FinancialEvidenceHead(t.Context(), a.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	aggregates, err := store.AggregateFinancialUsage(t.Context(), a.ID, start, end, head)
	if err != nil || len(aggregates) != 1 || aggregates[0].Quantity != 0 || aggregates[0].SourceCount != 3 || aggregates[0].Attribution.Name != "deleted-original" {
		t.Fatalf("closed period correction aggregate: %+v,%v", aggregates, err)
	}
}

// adr: 530 — immutable ledger, price history, atomic policy audit and privacy deletion.
func TestFinancialPostgresImmutableHistory(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a := financialAccount(t, store)
	start, end := financialPeriod()
	if err := store.AppendUsage(ctx, a.ID, uuid.NewString(), uuid.NewString(), start, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`update financial_usage_evidence set quantity=99 where account_id=$1`,
		`delete from financial_usage_evidence where account_id=$1`,
	} {
		if _, err := pool.Exec(ctx, query, a.ID); err == nil {
			t.Fatal("ledger rewrite accepted")
		}
	}
	price := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: end, Plan: api.PlanHobby, EffectiveFrom: start, DeliveryMode: "live", Price: financial.Price{Version: "fixed", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}}
	if _, err := store.PutFinancialPriceSnapshot(ctx, price); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update financial_price_snapshots set price='{}'::jsonb where account_id=$1`, a.ID); err == nil {
		t.Fatal("historical price rewrite accepted")
	}
	policy, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", financialBudgetSpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update financial_budget_revisions set actor='rewrite' where account_id=$1`, a.ID); err == nil {
		t.Fatal("budget audit rewrite accepted")
	}
	// Inject an audit failure: the corresponding intent write must roll back.
	if _, err := pool.Exec(ctx, `create function reject_financial_budget_test_audit() returns trigger language plpgsql as $$ begin raise exception 'audit unavailable'; end $$; create trigger reject_financial_budget_test_audit before insert on financial_budget_revisions for each row execute function reject_financial_budget_test_audit()`); err != nil {
		t.Fatal(err)
	}
	spec := financialBudgetSpec()
	spec.LimitMillicents = 2000
	if _, err := store.UpdateFinancialBudget(ctx, a.ID, policy.ID, 1, "editor", spec); err == nil {
		t.Fatal("policy committed without audit")
	}
	if _, err := pool.Exec(ctx, `drop trigger reject_financial_budget_test_audit on financial_budget_revisions; drop function reject_financial_budget_test_audit()`); err != nil {
		t.Fatal(err)
	}
	read, err := store.GetFinancialBudget(ctx, a.ID, policy.ID)
	if err != nil || !reflect.DeepEqual(read, policy) {
		t.Fatalf("audit failure did not roll back intent: %+v,%v", read, err)
	}
	if _, err := pool.Exec(ctx, `delete from accounts where id=$1`, a.ID); err != nil {
		t.Fatalf("privacy deletion blocked: %v", err)
	}
	head, err := store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil || head != 0 {
		t.Fatalf("privacy deletion retained evidence: %d,%v", head, err)
	}
	if err := store.AppendUsage(ctx, a.ID, uuid.NewString(), uuid.NewString(), start.Add(time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("late usage after account deletion: %v", err)
	}
	head, err = store.FinancialEvidenceHead(ctx, a.ID, start, end)
	if err != nil || head != 0 {
		t.Fatalf("late usage recreated erased financial history: %d,%v", head, err)
	}
}
