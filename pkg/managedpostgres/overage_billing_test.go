// adr: 20261031165104 — managed PostgreSQL overage is priced from cumulative
// usage and delivered in month-to-date steps on a compare-and-set ledger; the
// shadow sweep records what live would bill and closes a settled month.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPriceOverage(t *testing.T) {
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	month := 31 * 24 * time.Hour
	gib := int64(1) << 30
	for _, tc := range []struct {
		name                          string
		compute, storage              int64
		allowance, pool               int64
		asOf                          time.Time
		wantCompute, wantStorage      int64
		wantComputeOver, wantStoreOve int64
	}{
		{name: "inside both allowances", compute: 3600, storage: gib * 3600, allowance: 7200, pool: gib, asOf: october.Add(2 * time.Hour)},
		{name: "one CU-hour over", compute: 2 * 3600, allowance: 3600, asOf: october.Add(time.Hour),
			wantCompute: api.ManagedPostgresComputeOverageMillicentsPerCUHour, wantComputeOver: 3600},
		{name: "a CU-second floors", compute: 3601, allowance: 3600, asOf: october.Add(time.Hour), wantCompute: 5, wantComputeOver: 1},
		{name: "one GiB over for the whole month", storage: 2 * gib * int64(month/time.Second), pool: gib, asOf: october.Add(month),
			wantStorage: api.ManagedPostgresStorageOverageMillicentsPerGiBMonth, wantStoreOve: gib * int64(month/time.Second)},
		{name: "the pool counts only the month so far", storage: 2 * gib * 86400, pool: gib, asOf: october.Add(24 * time.Hour),
			wantStorage: api.ManagedPostgresStorageOverageMillicentsPerGiBMonth / 31, wantStoreOve: gib * 86400},
		{name: "past the month end prices the whole month", storage: gib * int64(month/time.Second), pool: gib, asOf: october.Add(40 * 24 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			charge, err := PriceOverage(UsageSnapshot{ComputeUnitSeconds: tc.compute, StorageByteSeconds: tc.storage}, tc.allowance, tc.pool, october, tc.asOf)
			if err != nil {
				t.Fatal(err)
			}
			if charge.ComputeMillicents != tc.wantCompute || charge.StorageMillicents != tc.wantStorage ||
				charge.ComputeOverUnitSeconds != tc.wantComputeOver || charge.StorageOverByteSeconds != tc.wantStoreOve ||
				charge.TotalMillicents != tc.wantCompute+tc.wantStorage {
				t.Fatalf("charge = %+v", charge)
			}
		})
	}
	if _, err := PriceOverage(UsageSnapshot{}, 0, 0, october, october.Add(-time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a point before the month = %v, want ErrInvalid", err)
	}
}

// billingTestStore serves chosen month usage per subject.
type billingTestStore struct {
	*MemoryStore
	usage map[string]map[time.Time]UsageSnapshot
}

func (s *billingTestStore) UsageSnapshot(_ context.Context, subject string, at time.Time) (UsageSnapshot, error) {
	return s.usage[subject][monthStart(at)], nil
}

func TestBillingSweeperShadowStepsAndClosesTheMonth(t *testing.T) {
	ctx := context.Background()
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	registry, memory := testRegistry(t, provider, nil), NewMemoryStore()
	if _, err := testService(t, registry, memory).Create(ctx, CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()}); err != nil {
		t.Fatal(err)
	}
	october, november := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	store := &billingTestStore{MemoryStore: memory, usage: map[string]map[time.Time]UsageSnapshot{
		"account-a": {october: {ComputeUnitSeconds: 2 * 3600}, november: {}},
	}}
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	mode, billable := BillingModeShadow, true
	sweeper, err := NewBillingSweeper(registry, store, BillingSweeperOptions{
		Policy: func() (BillingPolicy, bool) {
			return BillingPolicy{Provider: "polar", Mode: mode, EffectiveFrom: october}, true
		},
		Entitlement: func(context.Context, string) (OverageEntitlement, error) {
			return OverageEntitlement{Billable: billable, ComputeAllowanceSeconds: 3600, StoragePoolBytes: 1 << 30}, nil
		},
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	progress := func(period time.Time) BillingProgress {
		t.Helper()
		p, err := store.GetBillingProgress(ctx, "polar", "account-a", period, BillingModeShadow)
		if err != nil {
			t.Fatalf("progress %s: %v", period.Format("2006-01"), err)
		}
		return p
	}

	summary, err := sweeper.Sweep(ctx)
	if err != nil || summary.Stepped != 1 || summary.StepMillicents != api.ManagedPostgresComputeOverageMillicentsPerCUHour {
		t.Fatalf("first sweep = %+v, %v", summary, err)
	}
	if p := progress(october); p.DeliveredMillicents != 20_000 || p.PendingMillicents != 0 || p.ClosedAt != nil {
		t.Fatalf("october after one step = %+v", p)
	}
	clock = clock.Add(time.Hour)
	if summary, err := sweeper.Sweep(ctx); err != nil || summary.NotDue != 1 || summary.Stepped != 0 {
		t.Fatalf("a sweep inside the interval = %+v, %v", summary, err)
	}

	// The month ends with a third CU-hour; the first sweep after it settles
	// closes October at its final charge and opens November.
	store.usage["account-a"][october] = UsageSnapshot{ComputeUnitSeconds: 3 * 3600}
	clock = november.Add(api.ManagedPostgresBillingSettle)
	summary, err = sweeper.Sweep(ctx)
	if err != nil || summary.Closed != 1 || summary.Stepped != 1 || summary.StepMillicents != 20_000 {
		t.Fatalf("closing sweep = %+v, %v", summary, err)
	}
	if p := progress(october); p.DeliveredMillicents != 40_000 || p.ClosedAt == nil {
		t.Fatalf("closed october = %+v", p)
	}
	if p := progress(november); p.DeliveredMillicents != 0 || p.Version == 0 {
		t.Fatalf("november's first row = %+v", p)
	}
	store.usage["account-a"][october] = UsageSnapshot{ComputeUnitSeconds: 9 * 3600}
	clock = clock.Add(api.ManagedPostgresBillingProgressInterval)
	if summary, err := sweeper.Sweep(ctx); err != nil || summary.Closed != 0 || progress(october).DeliveredMillicents != 40_000 {
		t.Fatalf("a closed month changed: %+v, %v", summary, err)
	}

	// A subject that is no longer billable gets no new charge.
	billable = false
	clock = clock.Add(api.ManagedPostgresBillingProgressInterval)
	store.usage["account-a"][november] = UsageSnapshot{ComputeUnitSeconds: 5 * 3600}
	if summary, err := sweeper.Sweep(ctx); err != nil || summary.NotBillable != 1 || progress(november).DeliveredMillicents != 0 {
		t.Fatalf("a non-billable subject was charged: %+v, %v", summary, err)
	}

	// Live is step 3: until then a live policy must not acknowledge charges.
	mode = BillingModeLive
	if _, err := sweeper.Sweep(ctx); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("live sweep = %v, want ErrUnsupported", err)
	}
}

func TestBillingStepFinishesAFixedPendingStep(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := october.Add(time.Hour)
	// An earlier attempt fixed a 7,000 delta and stopped before completing it.
	p, err := store.SaveBillingProgress(ctx, BillingProgress{Provider: "polar", SubjectID: "account-a", PeriodStart: october, Mode: BillingModeShadow,
		DeliveredMillicents: 3_000, PendingMillicents: 7_000, PendingFixed: true}, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveBillingProgress(ctx, BillingProgress{Provider: "polar", SubjectID: "account-a", PeriodStart: october, Mode: BillingModeShadow}, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second insert = %v, want ErrConflict", err)
	}
	sweeper := &BillingSweeper{store: &billingTestStore{MemoryStore: store}}
	delta, err := sweeper.step(ctx, BillingPolicy{Provider: "polar", Mode: BillingModeShadow, EffectiveFrom: october}, "account-a", october, 12_000, at.Add(time.Hour), false)
	if err != nil || delta != 9_000 {
		t.Fatalf("step = %d, %v, want the fixed 7,000 and a new 2,000", delta, err)
	}
	if got, _ := store.GetBillingProgress(ctx, "polar", "account-a", october, BillingModeShadow); got.DeliveredMillicents != 12_000 || got.PendingMillicents != 0 || got.Version <= p.Version {
		t.Fatalf("progress = %+v", got)
	}
}
