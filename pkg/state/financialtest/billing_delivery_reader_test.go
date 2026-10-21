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
		})
	}
}
