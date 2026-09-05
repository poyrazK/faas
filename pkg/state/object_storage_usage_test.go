package state_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type accountingStore interface {
	recoveryStore
	state.ObjectStorageAccountingStore
}

func accountingPolicy() api.ObjectStoragePolicy {
	return api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 20, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
}

func seedAccounting(t *testing.T, st accountingStore) (state.ObjectBucket, api.ObjectStorageUsageReport) {
	t.Helper()
	ctx := context.Background()
	b := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	r := api.ObjectStorageUsageReport{AccountID: b.AccountID, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, Source: "qualified-provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)}
	if err := st.RecordObjectUsageReport(ctx, r); err != nil {
		t.Fatal(err)
	}
	return b, r
}

func TestObjectStorageAccountingMem(t *testing.T) { objectAccountingSuite(t, state.NewMemStore()) }
func TestObjectStorageAccountingPG(t *testing.T)  { st, _ := pgStore(t); objectAccountingSuite(t, st) }

func objectAccountingSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	p := accountingPolicy()
	b, report := seedAccounting(t, st)
	// Replicas cannot spend the same remaining account capacity twice.
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, uuid.NewString(), 10, true, p)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectCapacity) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 10 {
		t.Fatalf("capacity winners=%d, want 10", winners.Load())
	}
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u := state.SummarizeObjectUsage(snapshot, p, time.Now())
	if u.CapacityBytes != 100 || u.Authorizations != 10 || !u.Fresh {
		t.Fatal(u)
	}
	// Failed admission is not a provider request and consumes no new grant.
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "overflow", 1, true, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal(err)
	}
	// Reads do not consume storage capacity, but are budget checked.
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "read", 0, false, p); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal("duplicate report", err)
	}
	changed := report
	changed.CostMillicents = 1
	if err := st.RecordObjectUsageReport(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("conflicting duplicate", err)
	}
	report.ObservedAt = report.ObservedAt.Add(time.Second)
	report.CostMillicents = 100
	if err := st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	for _, put := range []bool{false, true} {
		if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "stopped", 0, put, p); !errors.Is(err, state.ErrObjectBudget) {
			t.Fatal("budget bypass", err)
		}
	}
	changed = report
	changed.ObservedAt = changed.ObservedAt.Add(time.Second)
	changed.CostMillicents = 0
	if err := st.RecordObjectUsageReport(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("budget regressed", err)
	}
	changed = report
	changed.ObservedAt = time.Now().Add(time.Hour)
	if err := st.RecordObjectUsageReport(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("future report", err)
	}
	// Missing next-month reports must not become a free budget reset.
	snapshot, err = st.ObjectUsage(ctx, b.AccountID, time.Now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if state.SummarizeObjectUsage(snapshot, p, time.Now().AddDate(0, 1, 0)).Fresh {
		t.Fatal("new month accepted missing usage")
	}
	// Cleanup is independent from accounting, and keeps historical costs.
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "delete", "deleted"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u = state.SummarizeObjectUsage(snapshot, p, time.Now())
	if u.CapacityBytes != 0 || u.CostMillicents != 100 {
		t.Fatal("deleted bucket refunded billed usage", u)
	}
	// Each new account starts unqualified; absence is never a zero report.
	c := seedRecoveryBucket(t, st)
	if err := st.AdmitObjectURL(ctx, c.AccountID, c.ID, "file", 1, true, p); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatal(err)
	}
}

func TestObjectStorageGrantAndInventoryMem(t *testing.T) { objectGrantSuite(t, state.NewMemStore()) }
func TestObjectStorageGrantAndInventoryPG(t *testing.T)  { st, _ := pgStore(t); objectGrantSuite(t, st) }

func objectGrantSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	for _, size := range []int64{10, 30, 10, 30} {
		if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "same-key", size, true, p); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u := state.SummarizeObjectUsage(snapshot, p, time.Now())
	if u.CapacityBytes != 30 || u.CapacityKeys != 1 {
		t.Fatal("replay/replacement double reservation", u)
	}
	if err := st.ClaimObjectInventory(ctx, b.ID, "next"); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimObjectInventory(ctx, b.ID, "competitor"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("concurrent inventory claim", err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, "wrong", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker committed", err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, "next", 0, 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u = state.SummarizeObjectUsage(snapshot, p, time.Now())
	if u.ObservedBytes != 0 || u.CapacityBytes != 30 {
		t.Fatal("empty inventory released replayable capacity", u)
	}
	p.MaxAccountKeys = 1
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "empty", 0, true, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal("zero-byte key budget bypass", err)
	}
	p = accountingPolicy()
	p.MaxMonthlyAuthorizations = 4
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "same-key", 10, true, p); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatal("authorization limiter", err)
	}
	p = accountingPolicy()
	snapshot.Reports[0].ObservedAt = time.Now().Add(-2 * time.Hour)
	if state.SummarizeObjectUsage(snapshot, p, time.Now()).Fresh {
		t.Fatal("stale report accepted")
	}
	snapshot.Reports[0].ObservedAt = time.Now().Add(-time.Minute)
	for i := range snapshot.Buckets {
		snapshot.Buckets[i].ObservedAt = time.Now().Add(-time.Hour)
	}
	if state.SummarizeObjectUsage(snapshot, p, time.Now()).Fresh {
		t.Fatal("stale inventory accepted")
	}
}

func TestObjectStorageProviderBudgetDimensionsMem(t *testing.T) {
	objectProviderBudgetDimensions(t, state.NewMemStore())
}

func TestObjectStorageProviderBudgetDimensionsPG(t *testing.T) {
	st, _ := pgStore(t)
	objectProviderBudgetDimensions(t, st)
}

func objectProviderBudgetDimensions(t *testing.T, st accountingStore) {
	for _, kind := range []string{"cost_millicents", "requests", "egress_bytes"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			b, report := seedAccounting(t, st)
			report.ObservedAt = report.ObservedAt.Add(time.Second)
			switch kind {
			case "cost_millicents":
				report.CostMillicents = 100
			case "requests":
				report.RequestCount = 100
			case "egress_bytes":
				report.EgressBytes = 100
			}
			if err := st.RecordObjectUsageReport(ctx, report); err != nil {
				t.Fatal(err)
			}
			for _, put := range []bool{false, true} {
				err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "blocked", 1, put, accountingPolicy())
				var limit *state.ObjectStorageLimitError
				if !errors.Is(err, state.ErrObjectBudget) || !errors.As(err, &limit) || limit.Kind != kind || limit.Observed != 100 || limit.Limit != 100 {
					t.Fatalf("wrong %s budget denial: %v", kind, err)
				}
			}
			snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Authorizations != 0 {
				t.Fatal("denied URL consumed issuance allowance")
			}
		})
	}
}

func TestObjectStorageAccountCapacityAcrossBucketsMem(t *testing.T) {
	accountCapacityAcrossBuckets(t, state.NewMemStore())
}
func TestObjectStorageAccountCapacityAcrossBucketsPG(t *testing.T) {
	st, _ := pgStore(t)
	accountCapacityAcrossBuckets(t, st)
}

func accountCapacityAcrossBuckets(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, report := seedAccounting(t, st)
	p := accountingPolicy()
	app, err := st.CreateApp(ctx, state.App{AccountID: b.AccountID, Slug: "second-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	c := b
	c.ID = uuid.NewString()
	c.AppID = app.ID
	c.PhysicalName = "gregale-" + uuid.NewString()
	c, err = st.ReserveObjectBucket(ctx, c, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimObjectBucket(ctx, c.AccountID, c.AppID, c.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, c.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimObjectInventory(ctx, c.ID, "scan"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, c.ID, "scan", 0, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for _, bucket := range []string{b.ID, c.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := st.AdmitObjectURL(ctx, b.AccountID, id, "file", 60, true, p)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectCapacity) {
				t.Error(err)
			}
		}(bucket)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("cross-app account capacity overspent", winners.Load())
	}
	bad := report
	bad.AccountID = "not-a-uuid"
	if err := st.RecordObjectUsageReport(ctx, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatal("invalid exporter UUID", err)
	}
	// A newly selected backend needs its own attributable report, not the
	// previous provider's costs silently reused as its zero usage.
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Buckets[1].Bucket.BackendID = "new-backend"
	if state.SummarizeObjectUsage(snapshot, p, time.Now()).Fresh {
		t.Fatal("provider switch reused old usage")
	}
}
