package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkConsumerCredits runs the ADR-1306 store contract: finalizing draws
// usable grants down oldest-expiring first, never past the statement or
// across currencies; voided grants draw nothing; spend checks gate the
// plan policy for their month only.
func checkConsumerCredits(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	credits := st.(state.APIConsumerCreditStore)
	caps := st.(state.APIConsumerSpendCapStore)
	statements := st.(state.APIConsumerUsageStatementStore)
	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "credit-consumer", "Credit customer")
	if err != nil {
		t.Fatal(err)
	}
	grant := func(currency string, amount int64, expires *time.Time) state.APIConsumerCreditGrant {
		t.Helper()
		g, err := credits.CreateAPIConsumerCreditGrant(ctx, state.APIConsumerCreditGrant{AccountID: accountID, AppID: appID,
			ConsumerID: consumer.ID, Currency: currency, AmountMillicents: amount, ExpiresAt: expires})
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
	if _, err := credits.VoidAPIConsumerCreditGrant(ctx, accountID, appID, consumer.ID, voided.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	created, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, Revision: 1, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		Currency: "EUR", BillableUnits: 4, AmountMillicents: 200, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 4, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 50, AmountMillicents: 200}},
	})
	if err != nil {
		t.Fatal(err)
	}
	final, _, err := statements.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, created.ID)
	if err != nil || final.CreditAppliedMillicents != 200 || final.AmountDueMillicents() != 0 {
		t.Fatalf("finalized = %+v, %v", final, err)
	}
	remaining := map[string]int64{}
	list, err := credits.ListAPIConsumerCreditGrants(ctx, accountID, appID, consumer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range list {
		remaining[g.ID] = g.RemainingMillicents
	}
	if remaining[expiring.ID] != 0 || remaining[lasting.ID] != 150 || remaining[voided.ID] != 0 {
		t.Fatalf("remaining after finalize = %v (expiring %s, lasting %s)", remaining, expiring.ID, lasting.ID)
	}
	// A statement with a base fee: credit pays the fee first, and only the
	// rest counts as paying usage (ADR-20261020211500).
	feeStart := start.Add(time.Hour)
	withFee, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, Revision: 1, PeriodStart: feeStart, PeriodEnd: feeStart.Add(time.Hour),
		Currency: "EUR", BillableUnits: 2, AmountMillicents: 200, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: feeStart, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 50, AmountMillicents: 100}},
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: uuid.NewString(), AmountMillicents: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if final, _, err := statements.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, withFee.ID); err != nil ||
		final.CreditAppliedMillicents != 150 {
		t.Fatalf("finalized with fee = %+v, %v", final, err)
	}
	var wantApplied int64
	if thisMonth := time.Now().UTC().Month(); start.Month() == thisMonth {
		wantApplied += 200
	}
	if time.Now().UTC().Month() == feeStart.Month() {
		wantApplied += 50
	}
	pos, err := credits.APIConsumerCreditPosition(ctx, accountID, appID, consumer.ID, "EUR", time.Now())
	if err != nil || pos.AvailableMillicents != 0 || pos.AppliedToUsageInMonthMillicents != wantApplied {
		t.Fatalf("position = %+v, %v; want applied to usage %d", pos, err, wantApplied)
	}

	if _, err := caps.PutAPIConsumerSpendCap(ctx, state.APIConsumerSpendCap{ConsumerID: consumer.ID, AccountID: accountID, AppID: appID,
		Currency: "EUR", MonthlyCapMillicents: 0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claimed, err := caps.ClaimAPIConsumerSpendCapChecks(ctx, now.Add(time.Second), time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if was, err := caps.RecordAPIConsumerSpendCheck(ctx, consumer.ID, monthStart, 75, true, now, now.Add(time.Minute)); err != nil || was {
		t.Fatalf("record = %v, %v", was, err)
	}
	policy, err := st.(state.APIConsumerPlanStore).GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumer.ID, now)
	if err != nil || !policy.SpendCapped || policy.SpendDueMillicents != 75 {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	if next, _ := st.(state.APIConsumerPlanStore).GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumer.ID, monthStart.AddDate(0, 1, 0)); next.SpendCapped {
		t.Fatal("last month's cap applied in the next month")
	}
	if err := caps.DeleteAPIConsumerSpendCap(ctx, accountID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := caps.GetAPIConsumerSpendCap(ctx, accountID, consumer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted cap = %v", err)
	}
}

// adr: 1306
func TestMemStoreConsumerCredits(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkConsumerCredits(t, ctx, store, accountID, appID)
}
