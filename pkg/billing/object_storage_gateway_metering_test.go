// adr: 20261024132844 — gateway-metered storage bills ADR-1430's rate.
package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGatewayMeteredMonthBillsADR1430Storage(t *testing.T) {
	period := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	end := period.AddDate(0, 1, 0)
	hours := int64(end.Sub(period).Hours())
	const gib = int64(1) << 30
	pricing := api.ObjectStoragePricing{Currency: api.BillingCurrency, StorageMillicentsPerGiBMonth: api.ObjectStorageMillicentsPerGiBMonth}
	hobby := api.MustLimitsFor(api.PlanHobby).ObjectStorage.Allowance()
	// Month close prices GiB-hours over a conventional 730-hour month and
	// removes the plan's included bytes held for those 730 hours (ADR-1320).
	beyond := func(gibHeld int64) int64 {
		excess := gibHeld*hours - 10*api.ObjectStorageBillingHoursPerMonth
		return (excess*api.ObjectStorageMillicentsPerGiBMonth + api.ObjectStorageBillingHoursPerMonth - 1) / api.ObjectStorageBillingHoursPerMonth
	}
	bucket := func(id string, byteHours int64, through time.Time) state.ObjectBucketUsage {
		return state.ObjectBucketUsage{Bucket: state.ObjectBucket{ID: id, AccountID: "acct", BackendID: "gcs", BackendFingerprint: "fp", State: "ready", CreatedAt: period.Add(-time.Hour)}, StoredByteHours: byteHours, StorageMeteredThrough: through}
	}
	for _, tc := range []struct {
		name    string
		buckets []state.ObjectBucketUsage
		want    int64
		err     error
	}{
		// 20 GiB held all month beyond Hobby's 10 GiB, at $0.03 per GiB-month.
		{name: "beyond the allowance", buckets: []state.ObjectBucketUsage{bucket("a", 20*gib*hours, end)}, want: beyond(20)},
		{name: "inside the allowance", buckets: []state.ObjectBucketUsage{bucket("a", 5*gib*hours, end)}, want: 0},
		{name: "never metered bucket bills nothing", buckets: []state.ObjectBucketUsage{bucket("a", 20*gib*hours, end), bucket("b", 0, time.Time{})}, want: beyond(20)},
		{name: "row not closed waits", buckets: []state.ObjectBucketUsage{bucket("a", gib, end.Add(-time.Minute))}, err: state.ErrObjectBillingIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"acct": {Buckets: tc.buckets}}}
			record, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, hobby, period, end.Add(time.Hour))
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if record.StorageMillicents != tc.want || record.RequestsMillicents != 0 || record.EgressMillicents != 0 {
				t.Fatalf("record = %+v, want storage %d", record, tc.want)
			}
		})
	}
	mixed := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"acct": {
		Buckets: []state.ObjectBucketUsage{bucket("a", gib, end)},
		Reports: []api.ObjectStorageUsageReport{{AccountID: "acct", BackendID: "gcs", BackendFingerprint: "fp", Source: "provider", PeriodStart: period, ObservedAt: end.Add(-time.Minute)}},
	}}}
	if _, err := FinalizeObjectStoragePeriod(context.Background(), mixed, "acct", pricing, hobby, period, end.Add(time.Hour)); !errors.Is(err, state.ErrObjectBillingConflict) {
		t.Fatalf("metered month with provider reports = %v, want conflict", err)
	}
}
