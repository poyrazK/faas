package financialtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// billingDeliveryTestStore lists the methods the suite calls rather than
// embedding state.Store (scripts/ci/teststorecheck).
type billingDeliveryTestStore interface {
	accountCreator
	state.BillingDeliveryReader
	state.BillingDeliverySourceRecorder
	RecordBillingMeterUsageDelivery(ctx context.Context, provider, accountID string, meter state.BillingMeter, windowStart time.Time, quantity int64) error
}

// adr: 20261019193600 — receipts sum per provider and account over hour windows, the
// first recorded quantity wins, and egress receipts are not compute.
func TestBillingDeliveryReaderStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store billingDeliveryTestStore = state.NewMemStore()
			if backend == "postgres" {
				pg, _, _ := financialPostgres(t)
				store = pg
			}
			a, other := financialAccount(t, store), financialAccount(t, store)
			start, end := financialPeriod()
			record := func(provider, account string, meter state.BillingMeter, hour time.Time, quantity int64) {
				t.Helper()
				if err := store.RecordBillingMeterUsageDelivery(t.Context(), provider, account, meter, hour, quantity); err != nil {
					t.Fatal(err)
				}
			}
			record("polar", a.ID, state.BillingMeterCompute, start.Add(time.Hour), 100)
			record("polar", a.ID, state.BillingMeterCompute, start.Add(time.Hour), 999) // replay keeps 100
			record("polar", a.ID, state.BillingMeterCompute, start.Add(2*time.Hour), 50)
			record("polar", a.ID, state.BillingMeterCompute, end, 1_000)               // next month
			record("paddle", a.ID, state.BillingMeterCompute, start.Add(time.Hour), 7) // other provider
			record("polar", other.ID, state.BillingMeterCompute, start.Add(time.Hour), 9)
			record("polar", a.ID, state.BillingMeterEgress, start.Add(3*time.Hour), 4096)
			got, err := store.DeliveredComputeMBSeconds(t.Context(), "polar", a.ID, start, end)
			if err != nil || got != 150 {
				t.Fatalf("delivered = %d, %v; want 150", got, err)
			}
			if got, err := store.DeliveredComputeMBSeconds(t.Context(), "polar", a.ID, start.Add(2*time.Hour), end); err != nil || got != 50 {
				t.Fatalf("delivered from hour 2 = %d, %v; want 50", got, err)
			}
			if _, err := store.DeliveredComputeMBSeconds(t.Context(), "polar", a.ID, end, start); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("inverted range: %v", err)
			}
			// Egress receipts sum apart from compute, first quantity wins.
			record("polar", a.ID, state.BillingMeterEgress, start.Add(3*time.Hour), 1) // replay keeps 4096
			record("polar", a.ID, state.BillingMeterEgress, start.Add(4*time.Hour), 0) // a settled zero hour
			record("polar", other.ID, state.BillingMeterEgress, start.Add(3*time.Hour), 8)
			record("polar", a.ID, state.BillingMeterEgress, end, 77) // next month
			if got, err := store.DeliveredEgressBytes(t.Context(), "polar", a.ID, start, end); err != nil || got != 4096 {
				t.Fatalf("delivered egress = %d, %v; want 4096", got, err)
			}
			if got, err := store.DeliveredEgressBytes(t.Context(), "paddle", a.ID, start, end); err != nil || got != 0 {
				t.Fatalf("delivered egress to paddle = %d, %v; want 0", got, err)
			}
			if _, err := store.DeliveredEgressBytes(t.Context(), "polar", a.ID, end, start); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("inverted egress range: %v", err)
			}

			// adr: 20261107105126 — receipts record the usage source that
			// quantified the hour; the first receipt wins, source included.
			ctx := t.Context()
			for _, meter := range []state.BillingMeter{state.BillingMeterCompute, state.BillingMeterEgress} {
				hour := start.Add(10 * time.Hour)
				if err := store.RecordBillingDeliveryFromSource(ctx, "polar", other.ID, meter, hour.Add(time.Minute), 5, state.UsageSourceLedger); err != nil {
					t.Fatal(err)
				}
				if err := store.RecordBillingDeliveryFromSource(ctx, "polar", other.ID, meter, hour, 6, state.UsageSourceUsageMinutes); err != nil {
					t.Fatal(err)
				}
				if got, err := store.BillingDeliverySource(ctx, "polar", other.ID, meter, hour); err != nil || got != state.UsageSourceLedger {
					t.Fatalf("%s source = %q, %v; want the first receipt's ledger", meter, got, err)
				}
				if _, err := store.BillingDeliverySource(ctx, "polar", other.ID, meter, hour.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("%s source without a receipt: %v", meter, err)
				}
				if err := store.RecordBillingDeliveryFromSource(ctx, "polar", other.ID, meter, hour.Add(2*time.Hour), 1, "sampled_rss"); err == nil {
					t.Fatalf("%s unknown source accepted", meter)
				}
			}
			if got, err := store.DeliveredComputeMBSeconds(ctx, "polar", other.ID, start, end); err != nil || got != 9+5 {
				t.Fatalf("other's delivered compute = %d, %v; want 14", got, err)
			}
			if got, err := store.DeliveredEgressBytes(ctx, "polar", other.ID, start, end); err != nil || got != 8+5 {
				t.Fatalf("other's delivered egress = %d, %v; want 13", got, err)
			}
			// A receipt written before receipts carried a source has none.
			if got, err := store.BillingDeliverySource(ctx, "polar", a.ID, state.BillingMeterCompute, start.Add(time.Hour)); err != nil || got != "" {
				t.Fatalf("legacy receipt source = %q, %v; want empty", got, err)
			}
		})
	}
}
