// adr: 20261107105126
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// counterTotal sums every series of the counter named name in reg.
func counterTotal(t *testing.T, reg prometheus.Gatherer, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

// When an operator corrects the ledger, the two usage sources disagree for
// that hour: ledger_shadow bills usage_minutes and counts the mismatch,
// ledger bills the corrected ledger and records it on the receipt, and off
// compares nothing.
func TestUsageSourceBillsTheChosenSourceAndCountsMismatches(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		source     meter.UsageSource
		deliveredH int64 // GB-h delivered for the hour
		compared   float64
		mismatched float64
		receipt    string
	}{
		{source: meter.UsageSourceOff, deliveredH: 10, receipt: state.UsageSourceUsageMinutes},
		{source: meter.UsageSourceLedgerShadow, deliveredH: 10, compared: 1, mismatched: 1, receipt: state.UsageSourceUsageMinutes},
		{source: meter.UsageSourceLedger, deliveredH: 4, compared: 1, mismatched: 1, receipt: state.UsageSourceLedger},
	} {
		t.Run(string(tc.source), func(t *testing.T) {
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			hour := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
			now := hour.Add(2 * time.Hour)
			putComputeGrant(t, ctx, store, acct, api.PlanHobby, hour.AddDate(0, 0, -14))
			included := api.IncludedMBSeconds(api.PlanHobby)
			// The month's allowance is used up before the hour; the hour adds
			// 10 GB-h, of which an operator correction takes 6 off the ledger.
			if err := store.AppendUsage(ctx, acct.ID, "app", "instance-a", hour.Add(-3*time.Hour), included, 0, 0, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			if err := store.AppendUsage(ctx, acct.ID, "app", "instance-b", hour.Add(5*time.Minute), 10*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			correctLedger(t, ctx, store, acct.ID, hour, -6*api.SecondsPerGBHour)
			completeUsageThrough(t, ctx, store, now)
			// The earlier hour was delivered already; only the corrected hour
			// is pending.
			if err := store.RecordBillingUsageDelivery(ctx, "stripe", acct.ID, hour.Add(-3*time.Hour), 0); err != nil {
				t.Fatal(err)
			}
			provider := &recordingOverage{}
			ops := wire.NewOpsMetrics("meter_test_usage_source")
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, ops).WithUsageSource(tc.source)
			if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			got, err := store.DeliveredComputeMBSeconds(ctx, "stripe", acct.ID, hour, hour.Add(time.Hour))
			if err != nil || got != tc.deliveredH*api.SecondsPerGBHour {
				t.Fatalf("delivered %d MB-s (%v), want %d GB-h", got, err, tc.deliveredH)
			}
			if source, err := store.BillingDeliverySource(ctx, "stripe", acct.ID, state.BillingMeterCompute, hour); err != nil || source != tc.receipt {
				t.Fatalf("receipt source = %q (%v), want %q", source, err, tc.receipt)
			}
			if got := counterTotal(t, ops.Registry(), "meterd_billing_usage_source_compared_windows_total"); got != tc.compared {
				t.Errorf("compared windows = %v, want %v", got, tc.compared)
			}
			if got := counterTotal(t, ops.Registry(), "meterd_billing_usage_source_mismatch_windows_total"); got != tc.mismatched {
				t.Errorf("mismatched windows = %v, want %v", got, tc.mismatched)
			}
		})
	}
}

// correctLedger appends an operator adjustment of quantity to the first
// compute evidence of account in the hour starting at hour.
func correctLedger(t *testing.T, ctx context.Context, store *state.MemStore, account string, hour time.Time, quantity int64) {
	t.Helper()
	monthStart := time.Date(hour.Year(), hour.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	head, err := store.FinancialEvidenceHead(ctx, account, monthStart, monthEnd)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListFinancialUsageEvidence(ctx, account, monthStart, monthEnd, 0, head, api.FinancialEvidencePageMax)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Evidence.Meter == string(state.BillingMeterCompute) && row.Evidence.Start.Truncate(time.Hour).Equal(hour) {
			if _, err := store.AppendFinancialAdjustment(ctx, state.FinancialAdjustment{AccountID: account, SourceID: "correction-" + row.Evidence.SourceID,
				CorrectsSourceID: row.Evidence.SourceID, Quantity: quantity, Actor: "operator@example.com", Reason: "replay test correction"}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no compute evidence for %s in the hour %s", account, hour)
}
