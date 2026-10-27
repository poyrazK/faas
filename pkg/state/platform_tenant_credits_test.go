package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkTenantCredits runs the ADR-20261019100331 store contract: finalizing
// a tenant statement draws usable grants down oldest-expiring first, never
// past the statement or across currencies; voided grants draw nothing; a
// spend check refuses the tenant for its month only.
func checkTenantCredits(t *testing.T, ctx context.Context, st state.Store, accountID string) {
	t.Helper()
	credits := st.(state.PlatformTenantCreditStore)
	caps := st.(state.PlatformTenantSpendCapStore)
	statements := st.(state.PlatformTenantStatementStore)
	tenant, _, err := st.(state.PlatformTenantStore).CreatePlatformTenant(ctx, accountID, "credit-tenant", "Credit tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	grant := func(currency string, amount int64, expires *time.Time) state.PlatformTenantCreditGrant {
		t.Helper()
		g, err := credits.CreatePlatformTenantCreditGrant(ctx, state.PlatformTenantCreditGrant{AccountID: accountID, TenantID: tenant.ID,
			Currency: currency, AmountMillicents: amount, ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	soon := time.Now().Add(time.Hour)
	lasting := grant("EUR", 300, nil)
	expiring := grant("EUR", 50, &soon)
	grant("USD", 1000, nil)
	voided := grant("EUR", 999, nil)
	if got, err := credits.VoidPlatformTenantCreditGrant(ctx, accountID, tenant.ID, voided.ID); err != nil || got.VoidedAt == nil ||
		got.RemainingMillicents != 0 {
		t.Fatalf("void = %+v, %v", got, err)
	}
	if _, err := credits.VoidPlatformTenantCreditGrant(ctx, accountID, tenant.ID, lasting.ID+"x"); err == nil {
		t.Fatal("voiding an unknown grant succeeded")
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	created, _, err := statements.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenant.ID,
		PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Revision: 1, Currency: "EUR", AmountMillicents: 200,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: uuid.NewString(), AmountMillicents: 200}}, AsOf: now, Repriced: true})
	if err != nil {
		t.Fatal(err)
	}
	final, _, err := statements.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, created.ID)
	if err != nil || final.CreditAppliedMillicents != 200 || final.AmountDueMillicents() != 0 {
		t.Fatalf("finalized = %+v, %v", final, err)
	}
	if again, _, err := statements.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, created.ID); err != nil ||
		again.CreditAppliedMillicents != 200 {
		t.Fatalf("refinalized = %+v, %v", again, err)
	}
	if got, err := statements.GetPlatformTenantStatement(ctx, accountID, tenant.ID, created.ID); err != nil || got.CreditAppliedMillicents != 200 {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	remaining := map[string]int64{}
	list, err := credits.ListPlatformTenantCreditGrants(ctx, accountID, tenant.ID)
	if err != nil || len(list) != 4 {
		t.Fatalf("grants = %+v, %v", list, err)
	}
	for _, g := range list {
		remaining[g.ID] = g.RemainingMillicents
	}
	if remaining[expiring.ID] != 0 || remaining[lasting.ID] != 150 || remaining[voided.ID] != 0 {
		t.Fatalf("remaining after finalize = %v (expiring %s, lasting %s)", remaining, expiring.ID, lasting.ID)
	}
	pos, err := credits.PlatformTenantCreditPosition(ctx, accountID, tenant.ID, "EUR", now)
	// The credit paid the base fee, so none of it counts against usage.
	if err != nil || pos.AvailableMillicents != 150 || pos.AppliedToUsageInMonthMillicents != 0 {
		t.Fatalf("position = %+v, %v", pos, err)
	}

	if _, err := caps.PutPlatformTenantSpendCap(ctx, state.PlatformTenantSpendCap{TenantID: tenant.ID, AccountID: accountID,
		Currency: "EUR", MonthlyCapMillicents: 0}); err != nil {
		t.Fatal(err)
	}
	claimed, err := caps.ClaimPlatformTenantSpendCapChecks(ctx, now.Add(time.Second), time.Minute, 10)
	if err != nil || len(claimed) != 1 || claimed[0].TenantID != tenant.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, err := caps.ClaimPlatformTenantSpendCapChecks(ctx, now.Add(time.Second), time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("claim during lease = %+v, %v", again, err)
	}
	if was, err := caps.RecordPlatformTenantSpendCheck(ctx, tenant.ID, monthStart, 75, true, now, now.Add(time.Minute)); err != nil || was {
		t.Fatalf("record = %v, %v", was, err)
	}
	budgets := st.(state.PlatformTenantRequestBudgetStore)
	decision, err := budgets.AdmitPlatformTenantRequest(ctx, accountID, tenant.ID)
	if err != nil || decision.Allowed || decision.Scope != state.PlatformTenantBudgetScopeSpendCap || decision.Limit != 0 ||
		decision.Observed != 75 || decision.SpendCurrency != "EUR" {
		t.Fatalf("capped tenant admission = %+v, %v", decision, err)
	}
	if was, err := caps.RecordPlatformTenantSpendCheck(ctx, tenant.ID, monthStart.AddDate(0, -1, 0), 75, false, now, now.Add(time.Minute)); err != nil || was {
		t.Fatalf("last month's check = %v, %v", was, err)
	}
	if decision, err := budgets.AdmitPlatformTenantRequest(ctx, accountID, tenant.ID); err != nil || !decision.Allowed {
		t.Fatalf("a cap checked for another month still refused: %+v, %v", decision, err)
	}
	if got, err := caps.GetPlatformTenantSpendCap(ctx, accountID, tenant.ID); err != nil || got.Capped || got.CappedIn(monthStart) {
		t.Fatalf("cap = %+v, %v", got, err)
	}
	if err := caps.DeletePlatformTenantSpendCap(ctx, accountID, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := caps.GetPlatformTenantSpendCap(ctx, accountID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted cap = %v", err)
	}
}

// adr: 20261019100331
func TestMemStoreTenantCredits(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	checkTenantCredits(t, ctx, store, accountID)
}
