package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkTenantControls runs the ADR-1319, 1322, 1324 and 1325 store
// contract: per-unit tenant cards with fees, tenant plans with fees, tenant
// meter units, trials, fee-only statements and the tenant overdue hold.
func checkTenantControls(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	tenant, _, err := st.(state.PlatformTenantStore).CreatePlatformTenant(ctx, accountID, "acme", "Acme", 250)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)

	trials := st.(state.PlatformTenantTrialStore)
	trial, err := trials.PutPlatformTenantTrial(ctx, state.PlatformTenantTrial{AccountID: accountID, TenantID: tenant.ID,
		StartsAt: start, EndsAt: start.AddDate(0, 0, 14)})
	if err != nil || !trial.Active(start) {
		t.Fatalf("trial = %+v, %v", trial, err)
	}
	if got, err := trials.GetPlatformTenantTrial(ctx, accountID, tenant.ID); err != nil || !got.EndsAt.Equal(trial.EndsAt) {
		t.Fatalf("get trial = %+v, %v", got, err)
	}
	if err := trials.DeletePlatformTenantTrial(ctx, accountID, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trials.GetPlatformTenantTrial(ctx, accountID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted trial: %v", err)
	}

	cards := st.(state.PlatformTenantRateCardStore)
	for _, unit := range []string{state.PlatformTenantRateCardUnitRequest, "app:tokens"} {
		if _, err := cards.CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{AccountID: accountID, TenantID: tenant.ID,
			Currency: "EUR", PriceMillicentsPerUnit: 10, Unit: unit, EffectiveFrom: start,
			BaseFeeMillicentsPerMonth: 5000, MinimumMillicentsPerMonth: 700, ProrateMonthlyFees: true}); err != nil {
			t.Fatalf("%s card at the shared minute: %v", unit, err)
		}
	}
	if _, err := cards.CreatePlatformTenantRateCardVersion(ctx, state.PlatformTenantRateCardInput{AccountID: accountID, TenantID: tenant.ID,
		Currency: "EUR", Unit: "app:tokens", EffectiveFrom: start}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second tokens card at one minute: %v", err)
	}
	listed, err := cards.ListPlatformTenantRateCards(ctx, accountID, tenant.ID)
	if err != nil || len(listed) != 2 || listed[0].BaseFeeMillicentsPerMonth != 5000 || listed[0].MinimumMillicentsPerMonth != 700 ||
		!listed[0].ProrateMonthlyFees {
		t.Fatalf("tenant cards = %+v, %v", listed, err)
	}

	plans := st.(state.PlatformTenantPlanStore)
	plan, err := plans.CreatePlatformTenantPlan(ctx, state.PlatformTenantPlan{AccountID: accountID, Name: "metered", Currency: "EUR",
		PriceMillicentsPerUnit: 2, Unit: "app:tokens", BaseFeeMillicentsPerMonth: 9000, MinimumMillicentsPerMonth: 100, ProrateMonthlyFees: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := plans.GetPlatformTenantPlan(ctx, accountID, plan.ID); err != nil || got.Unit != "app:tokens" ||
		got.BaseFeeMillicentsPerMonth != 9000 || got.MinimumMillicentsPerMonth != 100 || !got.ProrateMonthlyFees {
		t.Fatalf("plan = %+v, %v", got, err)
	}

	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	surface := uuid.NewString()
	usage := st.(state.ConsumerUsageStore)
	if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID, PlatformTenantSurfaceID: surface,
		WindowStart: minute, RequestCount: 1, BillableUnits: 1, MeterUnits: map[string]int64{"tokens": 420}}); err != nil {
		t.Fatal(err)
	}
	meters, err := st.(state.PlatformTenantMeterUsageStore).ListPlatformTenantMeterUsage(ctx, accountID, tenant.ID, "tokens", minute, minute.Add(time.Minute))
	if err != nil || len(meters) != 1 || meters[0].Units != 420 || meters[0].SourceKind != "surface" || meters[0].SubjectKey != surface {
		t.Fatalf("tenant meters = %+v, %v", meters, err)
	}

	statements := st.(state.PlatformTenantStatementStore)
	monthStart := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC)
	fees := []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: listed[0].ID, AmountMillicents: 5000}}
	statement, _, err := statements.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenant.ID,
		PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Revision: 1, Currency: "EUR", AmountMillicents: 5000,
		Fees: fees, AsOf: time.Now().UTC(), Repriced: true})
	if err != nil || len(statement.Fees) != 1 || statement.AmountMillicents != 5000 || len(statement.Lines) != 0 {
		t.Fatalf("fee-only statement = %+v, %v", statement, err)
	}
	if _, _, err := statements.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenant.ID,
		PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Revision: 2, PriorStatus: state.APIConsumerUsageStatementDraft,
		Currency: "EUR", AmountMillicents: 4000, Fees: fees, AsOf: time.Now().UTC(), Repriced: true}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("fees not matching the amount: %v", err)
	}
	finalized, _, err := statements.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := statements.GetPlatformTenantStatement(ctx, accountID, tenant.ID, finalized.ID); err != nil || len(got.Fees) != 1 {
		t.Fatalf("finalized fee statement = %+v, %v", got, err)
	}

	exports := st.(state.StatementInvoiceExportStore)
	export, _, err := exports.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourcePlatformTenant, StatementID: statement.ID, Provider: state.BillingProviderStripe,
		Customer: "cus_Acme", Currency: "EUR", AmountMinor: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := exports.SetStatementInvoiceExportInvoice(ctx, accountID, export.ID, "in_acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := exports.CompleteStatementInvoiceExport(ctx, accountID, export.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	due, next := now.AddDate(0, 0, -20), now.AddDate(0, 0, 1)
	if _, err := st.(state.StatementInvoicePaymentStore).RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 5, DueAt: &due}, now, &next); err != nil {
		t.Fatal(err)
	}
	overdue := st.(state.PlatformTenantOverdueStore)
	if _, err := overdue.PutPlatformTenantBillingPolicy(ctx, state.PlatformTenantBillingPolicy{AccountID: accountID, BlockOverdueAfterDays: 14}); err != nil {
		t.Fatal(err)
	}
	placed, _, err := overdue.SyncPlatformTenantOverdueHolds(ctx, now)
	if err != nil || len(placed) != 1 || placed[0].TenantID != tenant.ID || placed[0].ExportID != export.ID {
		t.Fatalf("tenant sync = %+v, %v", placed, err)
	}
	budgets := st.(state.PlatformTenantRequestBudgetStore)
	if decision, err := budgets.AdmitPlatformTenantRequest(ctx, accountID, tenant.ID); err != nil || decision.Allowed ||
		decision.Scope != state.PlatformTenantBudgetScopeOverdue || !decision.OverdueDueAt.Equal(due.Truncate(time.Microsecond)) {
		t.Fatalf("held tenant admission = %+v, %v", decision, err)
	}
	if holds, err := overdue.ListPlatformTenantOverdueHolds(ctx, accountID); err != nil || len(holds) != 1 {
		t.Fatalf("holds = %+v, %v", holds, err)
	}
	if err := overdue.DeletePlatformTenantBillingPolicy(ctx, accountID); err != nil {
		t.Fatal(err)
	}
	if _, lifted, err := overdue.SyncPlatformTenantOverdueHolds(ctx, now); err != nil || len(lifted) != 1 {
		t.Fatalf("tenant lift = %+v, %v", lifted, err)
	}
	if decision, err := budgets.AdmitPlatformTenantRequest(ctx, accountID, tenant.ID); err != nil || !decision.Allowed {
		t.Fatalf("released tenant admission = %+v, %v", decision, err)
	}
}

// adr: 1319, 1322, 1324, 1325
func TestMemStoreTenantControls(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkTenantControls(t, ctx, store, accountID, appID)
}

// adr: 1319, 1322, 1324, 1325
func TestPgStoreTenantControls(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkTenantControls(t, ctx, store, accountID, appID)
}
