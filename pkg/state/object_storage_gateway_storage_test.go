// adr: 20261024132844

package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const testGiB = int64(1) << 30

// observeBucketBytes publishes an inventory of bytes for b.
func observeBucketBytes(t *testing.T, st accountingStore, b state.ObjectBucket, bytes int64) {
	t.Helper()
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(t.Context(), b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(t.Context(), b.ID, token, bytes, 1); err != nil {
		t.Fatal(err)
	}
}

func meteredBucket(t *testing.T, st accountingStore, account, bucket string, at time.Time) state.ObjectBucketUsage {
	t.Helper()
	snapshot, err := st.(state.ObjectStorageBillingStore).ObjectUsageForPeriod(t.Context(), account, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range snapshot.Buckets {
		if b.Bucket.ID == bucket {
			return b
		}
	}
	t.Fatal("bucket missing from usage", bucket)
	return state.ObjectBucketUsage{}
}

func TestGatewayStorageAccruesAcrossTheMonthMem(t *testing.T) {
	m := state.NewMemStore()
	// The shared seed records a provider report for the real current month;
	// metering a distant month keeps the two sources apart.
	now := time.Date(2030, time.October, 31, 22, 0, 0, 0, time.UTC)
	m.SetClockForTest(func() time.Time { return now })
	b, _ := seedAccounting(t, m)
	observeBucketBytes(t, m, b, testGiB)
	accrue := func(at time.Time) {
		t.Helper()
		now = at
		if err := m.AccrueObjectStorageGatewayStorage(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	october := time.Date(2030, time.October, 1, 0, 0, 0, 0, time.UTC)
	november := october.AddDate(0, 1, 0)
	accrue(now) // opens October at 22:00: nothing before it was metered
	accrue(now.Add(time.Hour))
	if got := meteredBucket(t, m, b.AccountID, b.ID, october); got.StoredByteHours != testGiB || !got.StorageMeteredThrough.Equal(now) {
		t.Fatalf("one hour of 1 GiB = %d through %v", got.StoredByteHours, got.StorageMeteredThrough)
	}
	accrue(november.Add(time.Hour))
	oct := meteredBucket(t, m, b.AccountID, b.ID, october)
	nov := meteredBucket(t, m, b.AccountID, b.ID, november)
	if oct.StoredByteHours != 2*testGiB || !oct.StorageMeteredThrough.Equal(november) {
		t.Fatalf("October closed at %d through %v, want 2 GiB-hours through month end", oct.StoredByteHours, oct.StorageMeteredThrough)
	}
	if nov.StoredByteHours != testGiB {
		t.Fatalf("November continued at %d, want 1 GiB-hour from the month start", nov.StoredByteHours)
	}
	snapshot, err := m.ObjectUsageForPeriod(t.Context(), b.AccountID, october)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := state.SummarizeObjectStorageBillingUsage(snapshot, october)
	if err != nil || usage.StoredByteHours != 2*testGiB {
		t.Fatalf("October bills %d, err %v", usage.StoredByteHours, err)
	}
}

func TestGatewayStorageAccrualCountsTimeOncePG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	observeBucketBytes(t, st, b, testGiB)
	if err := st.AccrueObjectStorageGatewayStorage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_gateway_storage SET accrued_from = now() - interval '1 hour', accrued_through = now() - interval '1 hour' WHERE bucket_id = $1`, b.ID); err != nil {
		t.Skip("cannot rewind the metered hour this early in the month:", err)
	}
	// Two apid processes accrue the same hour; it must be counted once.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.AccrueObjectStorageGatewayStorage(context.WithoutCancel(ctx))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := meteredBucket(t, st, b.AccountID, b.ID, time.Now())
	if got.StoredByteHours < testGiB || got.StoredByteHours > testGiB+testGiB/60 || got.StorageMeteredThrough.IsZero() {
		t.Fatalf("one hour of 1 GiB = %d byte-hours through %v", got.StoredByteHours, got.StorageMeteredThrough)
	}
}

func TestGatewaySafetyReportsMeteredStorage(t *testing.T) {
	now := time.Now().UTC()
	p := gatewaySafetyPolicy()
	live := func(id string, byteHours int64, through time.Time) state.ObjectBucketUsage {
		return state.ObjectBucketUsage{Bucket: state.ObjectBucket{ID: id, State: "ready", CreatedAt: now.Add(-time.Second)}, ObservedAt: now, GatewayMetricsKnown: true, StoredByteHours: byteHours, StorageMeteredThrough: through}
	}
	metered := state.SummarizeObjectUsage(state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{live("a", 5, now), live("b", 7, now)}}, p, now)
	if metered.StoredByteHours != 12 || len(metered.UnavailableMeters) != 1 || metered.UnavailableMeters[0] != "cost_millicents" || !metered.Chargeable() {
		t.Fatalf("metered usage = %+v", metered)
	}
	partial := state.SummarizeObjectUsage(state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{live("a", 5, now), live("b", 0, time.Time{})}}, p, now)
	if len(partial.UnavailableMeters) != 2 || partial.Chargeable() {
		t.Fatalf("partly metered usage claimed to be chargeable: %+v", partial)
	}
	if !(api.ObjectStoragePricing{Currency: api.BillingCurrency, StorageMillicentsPerGiBMonth: api.ObjectStorageMillicentsPerGiBMonth}).StorageOnly() {
		t.Fatal("ADR-1430 rate card is not storage-only")
	}
}
