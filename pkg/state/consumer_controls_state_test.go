package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkConsumerControls runs the ADR-1314–1318 store contract: trials, the
// overdue sync, statement fees, app meter units and per-unit card versions.
func checkConsumerControls(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "controls", "Controls")
	if err != nil {
		t.Fatal(err)
	}
	trials := st.(state.APIConsumerTrialStore)
	start := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	trial, err := trials.PutAPIConsumerTrial(ctx, state.APIConsumerTrial{AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		StartsAt: start, EndsAt: start.AddDate(0, 0, 7)})
	if err != nil || !trial.Active(start) || trial.Active(start.AddDate(0, 0, 7)) {
		t.Fatalf("trial = %+v, %v", trial, err)
	}
	if got, err := trials.GetAPIConsumerTrial(ctx, accountID, appID, consumer.ID); err != nil || !got.EndsAt.Equal(trial.EndsAt) {
		t.Fatalf("get trial = %+v, %v", got, err)
	}
	if err := trials.DeleteAPIConsumerTrial(ctx, accountID, appID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trials.GetAPIConsumerTrial(ctx, accountID, appID, consumer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted trial: %v", err)
	}

	cards := st.(state.APIConsumerRateCardStore)
	for _, unit := range []string{state.APIConsumerRateCardUnitRequest, "app:tokens"} {
		if _, err := cards.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID, AppID: appID,
			Currency: "EUR", PriceMillicentsPerUnit: 10, Unit: unit, EffectiveFrom: start, BaseFeeMillicentsPerMonth: 1000}); err != nil {
			t.Fatalf("%s card at the shared minute: %v", unit, err)
		}
	}
	if _, err := cards.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: accountID, AppID: appID,
		Currency: "EUR", Unit: "app:tokens", EffectiveFrom: start}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second tokens card at one minute: %v", err)
	}

	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	usage := st.(state.ConsumerUsageStore)
	if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 1, BillableUnits: 1, MeterUnits: map[string]int64{"tokens": 900}}); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 1, MeterUnits: map[string]int64{"tokens": 1}}); err == nil {
		t.Fatal("an unbillable request reported units")
	}
	meters, err := st.(state.ConsumerMeterUsageStore).ListAPIConsumerMeterUsage(ctx, accountID, appID, consumer.ID, "tokens", minute, minute.Add(time.Minute))
	if err != nil || len(meters) != 1 || meters[0].Units != 900 {
		t.Fatalf("meters = %+v, %v", meters, err)
	}

	statements := st.(state.APIConsumerUsageStatementStore)
	monthStart := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC)
	cardID := uuid.NewString()
	statement, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{AccountID: accountID, AppID: appID,
		ConsumerID: consumer.ID, PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Revision: 1, Currency: "EUR",
		AmountMillicents: 375_000, Priced: true, AsOf: time.Now().UTC(),
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: cardID, AmountMillicents: 375_000}}})
	if err != nil || len(statement.Fees) != 1 || statement.Fees[0].AmountMillicents != 375_000 {
		t.Fatalf("fee statement = %+v, %v", statement, err)
	}
	if _, _, err := statements.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID); err != nil {
		t.Fatal(err)
	}

	exports := st.(state.StatementInvoiceExportStore)
	export, _, err := exports.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: statement.ID, Provider: state.BillingProviderStripe,
		Customer: "cus_Late", Currency: "EUR", AmountMinor: 375})
	if err != nil {
		t.Fatal(err)
	}
	if err := exports.SetStatementInvoiceExportInvoice(ctx, accountID, export.ID, "in_late"); err != nil {
		t.Fatal(err)
	}
	if _, err := exports.CompleteStatementInvoiceExport(ctx, accountID, export.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	due, next := now.AddDate(0, 0, -20), now.AddDate(0, 0, 1)
	if _, err := st.(state.StatementInvoicePaymentStore).RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 375, DueAt: &due}, now, &next); err != nil {
		t.Fatal(err)
	}
	overdue := st.(state.APIConsumerOverdueStore)
	if _, err := overdue.PutAPIConsumerBillingPolicy(ctx, state.APIConsumerBillingPolicy{AccountID: accountID, AppID: appID, BlockOverdueAfterDays: 14}); err != nil {
		t.Fatal(err)
	}
	placed, lifted, err := overdue.SyncAPIConsumerOverdueHolds(ctx, now)
	if err != nil || len(placed) != 1 || len(lifted) != 0 || placed[0].ConsumerID != consumer.ID || placed[0].ExportID != export.ID {
		t.Fatalf("sync = %+v, %+v, %v", placed, lifted, err)
	}
	if again, _, err := overdue.SyncAPIConsumerOverdueHolds(ctx, now); err != nil || len(again) != 0 {
		t.Fatalf("second sync placed %+v, %v", again, err)
	}
	policy, err := st.(state.APIConsumerPlanStore).GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumer.ID, now)
	if err != nil || !policy.OverdueBlocked {
		t.Fatalf("plan policy = %+v, %v", policy, err)
	}
	if err := overdue.DeleteAPIConsumerBillingPolicy(ctx, accountID, appID); err != nil {
		t.Fatal(err)
	}
	if _, lifted, err := overdue.SyncAPIConsumerOverdueHolds(ctx, now); err != nil || len(lifted) != 1 {
		t.Fatalf("lift = %+v, %v", lifted, err)
	}
}

// adr: 1314, 1315, 1316, 1317, 1318
func TestMemStoreConsumerControls(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkConsumerControls(t, ctx, store, accountID, appID)
}
