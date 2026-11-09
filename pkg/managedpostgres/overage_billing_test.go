// adr: 20261031110037 — managed PostgreSQL overage is charged above the
// allowance, up to the ceiling, in idempotent cumulative steps.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testAllowance = 25 * 3600 // Hobby: 25 CU-h

func TestOverageChargeIsBoundedByTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name               string
		used, allowance    int64
		billed, millicents int64
	}{
		{name: "within the allowance", used: testAllowance, allowance: testAllowance},
		{name: "two hours over", used: testAllowance + 2*3600, allowance: testAllowance, billed: 2 * 3600, millicents: 2 * api.ManagedPostgresOverageMillicentsPerComputeUnitHour},
		{name: "a fraction of a millicent is not charged", used: testAllowance + 1, allowance: testAllowance, billed: 1, millicents: api.ManagedPostgresOverageMillicentsPerComputeUnitHour / 3600},
		{name: "past the ceiling", used: 5 * testAllowance, allowance: testAllowance, billed: testAllowance, millicents: 25 * api.ManagedPostgresOverageMillicentsPerComputeUnitHour},
		{name: "no allowance", used: 10, allowance: 0},
	} {
		billed, millicents := OverageCharge(tc.used, tc.allowance)
		if billed != tc.billed || millicents != tc.millicents {
			t.Errorf("%s: OverageCharge = %d CU-s, %d millicents; want %d, %d", tc.name, billed, millicents, tc.billed, tc.millicents)
		}
	}
	// Hobby's whole overage room is its 25 CU-hours at the overage price,
	// $5.8275 (ADR-20261102072425).
	if _, millicents := OverageCharge(2*testAllowance, testAllowance); millicents != 582_750 {
		t.Fatalf("Hobby overage room = %d millicents, want 582750", millicents)
	}
}

type overageUsageStore struct {
	*MemoryStore
	used   map[string]int64
	stored map[string]int64 // storage byte-seconds
}

func (s *overageUsageStore) UsageSnapshot(_ context.Context, subjectID string, at time.Time) (UsageSnapshot, error) {
	key := subjectID + monthStart(at).Format("2006-01")
	return UsageSnapshot{PeriodStart: monthStart(at), ComputeUnitSeconds: s.used[key], StorageByteSeconds: s.stored[key]}, nil
}

type overageFixture struct {
	store     *overageUsageStore
	now       time.Time
	limits    CapLimits
	monthly   map[string]CapLimits
	published []OverageChargeStep
	fail      error
	// lostResponse makes the next publish reach the provider and then fail,
	// as a timeout or an apid restart after the provider accepted it does.
	lostResponse bool
}

func newOverageFixture(t *testing.T, now time.Time) *overageFixture {
	t.Helper()
	return &overageFixture{store: &overageUsageStore{MemoryStore: NewMemoryStore(), used: map[string]int64{}, stored: map[string]int64{}}, now: now,
		limits: CapLimits{Allowance: testAllowance, Ceiling: 2 * testAllowance, Overage: true}}
}

func (f *overageFixture) biller(t *testing.T, live bool, from time.Time) *OverageBiller {
	t.Helper()
	options := OverageBillerOptions{Provider: "polar", EffectiveFrom: from, Now: func() time.Time { return f.now },
		Allowance: func(_ context.Context, subject string, month time.Time) (CapLimits, error) {
			if subject == "gone" {
				return CapLimits{}, ErrNotFound
			}
			if limits, ok := f.monthly[month.Format("2006-01")]; ok {
				return limits, nil
			}
			return f.limits, nil
		}}
	if live {
		options.Publish = func(_ context.Context, step OverageChargeStep) error {
			if f.fail != nil {
				return f.fail
			}
			f.published = append(f.published, step)
			if f.lostResponse {
				f.lostResponse = false
				return errors.New("provider response lost")
			}
			return nil
		}
	}
	biller, err := NewOverageBiller(f.store, options)
	if err != nil {
		t.Fatal(err)
	}
	return biller
}

func (f *overageFixture) reachAllowance(t *testing.T, subject string, month time.Time, overage bool) {
	t.Helper()
	if _, err := f.store.RecordCapEvent(context.Background(), CapEvent{SubjectID: subject, PeriodStart: month, Threshold: CapThresholdAllowance,
		ReachedAt: month.Add(time.Hour), Used: testAllowance, Allowance: testAllowance, Ceiling: 2 * testAllowance, Overage: overage}); err != nil {
		t.Fatal(err)
	}
}

func TestOverageBillerDeliversCumulativeStepsOnce(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	biller := f.biller(t, true, november)
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	if summary, err := biller.Deliver(ctx); err != nil || summary.Delivered != 1 || summary.Millicents != 2*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("first pass = %+v, %v", summary, err)
	}
	if len(f.published) != 1 || f.published[0].DeliveredMillicents != 0 || f.published[0].TargetMillicents != 2*api.ManagedPostgresOverageMillicentsPerComputeUnitHour || f.published[0].BilledUnitSeconds != 2*3600 {
		t.Fatalf("published %+v", f.published)
	}
	if summary, err := biller.Deliver(ctx); err != nil || summary.Delivered != 0 || len(f.published) != 1 {
		t.Fatalf("unchanged usage charged again: %+v, %v", summary, err)
	}
	f.store.used["acct2026-11"] = testAllowance + 3*3600
	if _, err := biller.Deliver(ctx); err != nil || len(f.published) != 2 || f.published[1].DeliveredMillicents != 2*api.ManagedPostgresOverageMillicentsPerComputeUnitHour || f.published[1].TargetMillicents != 3*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("second step %+v, %v", f.published, err)
	}
	// Usage past the ceiling is charged only up to it.
	f.store.used["acct2026-11"] = 10 * testAllowance
	if _, err := biller.Deliver(ctx); err != nil || f.published[2].TargetMillicents != 25*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("ceiling step %+v, %v", f.published, err)
	}
	delivery, err := f.store.OverageDelivered(ctx, "polar", "acct", november)
	if err != nil || !delivery.Live || delivery.Millicents != 25*api.ManagedPostgresOverageMillicentsPerComputeUnitHour || delivery.BilledUnitSeconds != testAllowance {
		t.Fatalf("recorded %+v, %v", delivery, err)
	}
}

func TestOverageBillerChargesOnlyOptedInSubjects(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.limits.Overage = false
	f.reachAllowance(t, "off", november, false)
	f.reachAllowance(t, "was-on", november, true)
	f.reachAllowance(t, "gone", november, true)
	for _, subject := range []string{"off", "was-on", "gone"} {
		f.store.used[subject+"2026-11"] = testAllowance + 3600
	}
	summary, err := f.biller(t, true, november).Deliver(ctx)
	if err != nil || summary.Delivered != 1 || len(f.published) != 1 || f.published[0].SubjectID != "was-on" {
		t.Fatalf("summary %+v, published %+v, %v; want only the subject that reached its allowance with overage on", summary, f.published, err)
	}
}

func TestOverageBillerRetriesAFailedStepAndShadowsWithoutSending(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 3600
	f.fail = errors.New("provider down")
	if summary, err := f.biller(t, true, november).Deliver(ctx); err == nil || summary.Failed != 1 {
		t.Fatalf("failed publish = %+v, %v", summary, err)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.Millicents != 0 {
		t.Fatalf("a failed step was recorded: %+v", delivery)
	}
	f.fail = nil
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 1 || f.published[0].TargetMillicents != api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("retry %+v, %v", f.published, err)
	}

	shadow := newOverageFixture(t, november.Add(10*24*time.Hour))
	shadow.reachAllowance(t, "acct", november, true)
	shadow.store.used["acct2026-11"] = testAllowance + 3600
	summary, err := shadow.biller(t, false, november).Deliver(ctx)
	if err != nil || summary.Shadowed != 1 || len(shadow.published) != 0 {
		t.Fatalf("shadow = %+v, %v, published %v", summary, err, shadow.published)
	}
	if delivery, _ := shadow.store.OverageDelivered(ctx, "polar", "acct", november); delivery.Live || delivery.Millicents != api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("shadow recorded %+v", delivery)
	}
}

// Bug hunt 33: after a publish the provider accepted but whose response was
// lost, the next sweep saw more usage, so its step had a new target and a new
// external ID and charged from the same starting point again. The step now
// keeps its starting point, which the provider de-duplicates on.
func TestOverageBillerRetryAfterALostResponseKeepsItsStartingPoint(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 3600
	f.lostResponse = true
	if _, err := f.biller(t, true, november).Deliver(ctx); err == nil {
		t.Fatal("a lost response was reported as delivered")
	}
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	// The step was reserved before it was sent (ADR-20261101143527): the
	// retry resends it unchanged, so the provider keeps one copy and the
	// recorded charge is the one it holds.
	if len(f.published) != 2 || f.published[1] != f.published[0] || f.published[0].DeliveredMillicents != 0 || f.published[0].TargetMillicents != hour {
		t.Fatalf("published %+v; want the reserved step resent unchanged, so the provider keeps one", f.published)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); !delivery.Live || delivery.Millicents != hour {
		t.Fatalf("recorded %+v", delivery)
	}
	// The usage that grew meanwhile is charged from what was recorded, not
	// dropped as a duplicate of the first step.
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.published) != 3 || f.published[2].DeliveredMillicents != hour || f.published[2].TargetMillicents != 2*hour {
		t.Fatalf("published %+v; want the growth charged from %d", f.published, hour)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.Millicents != 2*hour {
		t.Fatalf("recorded %+v", delivery)
	}
}

// Bug hunt 33: shadow and live share the month's row. Going live counted
// what shadow recorded as already charged; shadow after live would hide the
// live charge.
func TestOverageBillerKeepsShadowAndLiveApart(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	if summary, err := f.biller(t, false, november).Deliver(ctx); err != nil || summary.Shadowed != 1 {
		t.Fatalf("shadow = %+v, %v", summary, err)
	}
	f.store.used["acct2026-11"] = testAllowance + 3*3600
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 1 || summary.Millicents != 3*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("live after shadow = %+v, %v; want the whole month charged", summary, err)
	}
	if len(f.published) != 1 || f.published[0].DeliveredMillicents != 0 || f.published[0].TargetMillicents != 3*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("published %+v; want a step from 0, not from the shadow amount", f.published)
	}
	f.store.used["acct2026-11"] = testAllowance + 4*3600
	if summary, err := f.biller(t, false, november).Deliver(ctx); err != nil || summary.Shadowed != 0 {
		t.Fatalf("shadow after live = %+v, %v; want nothing recorded", summary, err)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); !delivery.Live || delivery.Millicents != 3*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("recorded %+v; want the live charge kept", delivery)
	}
}

func TestOverageBillerClosesThePreviousMonthInsideTheWindow(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	december := november.AddDate(0, 1, 0)
	f := newOverageFixture(t, december.Add(2*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	// A late usage window raised November's total after the month ended.
	f.store.used["acct2026-11"] = testAllowance + 3600
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 1 || !f.published[0].PeriodStart.Equal(november) {
		t.Fatalf("close step %+v, %v", f.published, err)
	}
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	f.now = december.Add(api.ManagedPostgresOverageCloseWindow)
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 1 {
		t.Fatalf("a closed month was charged after its window: %+v, %v", f.published, err)
	}
	// A month before the effective date is never charged.
	late := newOverageFixture(t, december.Add(time.Hour))
	late.reachAllowance(t, "acct", november, true)
	late.store.used["acct2026-11"] = testAllowance + 3600
	if _, err := late.biller(t, true, december).Deliver(ctx); err != nil || len(late.published) != 0 {
		t.Fatalf("charged before the effective month: %+v, %v", late.published, err)
	}
}

func TestOverageDeliveryAdvancesOnlyFromThePreviousCharge(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	delivery := OverageDelivery{Provider: "polar", SubjectID: "acct", PeriodStart: november, Live: true, BilledUnitSeconds: 3600, Millicents: 20_000, UpdatedAt: november.Add(time.Hour)}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{}); err != nil || !recorded {
		t.Fatalf("first = %v, %v", recorded, err)
	}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{}); err != nil || recorded {
		t.Fatalf("a replica that lost the race recorded: %v, %v", recorded, err)
	}
	delivery.Millicents = 10_000
	if _, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{Live: true, Millicents: 20_000}); err == nil {
		t.Fatal("a lower charge was accepted")
	}
	delivery.PeriodStart = november.Add(time.Hour)
	delivery.Millicents = 30_000
	if _, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{Live: true, Millicents: 20_000}); err == nil {
		t.Fatal("a mid-month period was accepted")
	}
}

const (
	testGiB          = int64(1 << 30)
	testNovemberSecs = int64(30 * 24 * 3600)
)

// Storage is charged on the month's average past the pool, at most one more
// pool (ADR-20261031110037 amendment 2).
func TestStorageOverageCharge(t *testing.T) {
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	price := int64(api.ManagedPostgresStorageOverageMillicentsPerGiBMonth)
	for _, tc := range []struct {
		name                   string
		used, pool             int64
		billed, wantMillicents int64
	}{
		{"inside the pool on average", 2*testGiB*testNovemberSecs - 1, 2 * testGiB, 0, 0},
		{"one GiB past the pool all month", 3 * testGiB * testNovemberSecs, 2 * testGiB, testGiB * testNovemberSecs, price},
		{"past the doubled pool is charged one pool", 10 * testGiB * testNovemberSecs, 2 * testGiB, 2 * testGiB * testNovemberSecs, 2 * price},
		{"a fraction of a millicent is not charged", 2*testGiB*testNovemberSecs + 1, 2 * testGiB, 1, 0},
		{"no pool", testGiB * testNovemberSecs, 0, 0, 0},
	} {
		billed, millicents := StorageOverageCharge(tc.used, tc.pool, november)
		if billed != tc.billed || millicents != tc.wantMillicents {
			t.Errorf("%s: StorageOverageCharge = %d byte-s, %d millicents; want %d, %d", tc.name, billed, millicents, tc.billed, tc.wantMillicents)
		}
	}
	// A GiB-month is priced over the month's own seconds.
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if _, millicents := StorageOverageCharge(3*testGiB*31*24*3600, 2*testGiB, december); millicents != price {
		t.Fatalf("a GiB past the pool through December = %d millicents, want %d", millicents, price)
	}
}

// With overage on, storage past the plan's pool is charged on the step even
// while compute stays inside the allowance. Turning overage off keeps a
// month's storage charge, so the cumulative charge never drops; without
// overage, storage is never charged.
func TestOverageBillerChargesStoragePastThePool(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	price := int64(api.ManagedPostgresStorageOverageMillicentsPerGiBMonth)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.limits.StorageAllowance = testGiB
	if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
		t.Fatal(err)
	}
	f.store.used["acct2026-11"] = testAllowance / 2
	f.store.stored["acct2026-11"] = 2 * testGiB * testNovemberSecs
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 1 || len(f.published) != 1 {
		t.Fatalf("storage step = %+v, %v, published %+v", summary, err, f.published)
	}
	if step := f.published[0]; step.BilledUnitSeconds != 0 || step.BilledStorageByteSeconds != testGiB*testNovemberSecs ||
		step.StoragePoolBytes != testGiB || step.TargetMillicents != price {
		t.Fatalf("storage step %+v", step)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.BilledStorageByteSeconds != testGiB*testNovemberSecs {
		t.Fatalf("delivery %+v", delivery)
	}

	f.limits.Overage = false
	f.store.used["acct2026-11"] = testAllowance + 3600
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 2 ||
		f.published[1].TargetMillicents != price+api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("after overage off: %+v, %v", f.published, err)
	}

	off := newOverageFixture(t, november.Add(10*24*time.Hour))
	off.limits.Overage, off.limits.StorageAllowance = false, testGiB
	off.reachAllowance(t, "acct", november, false)
	off.store.used["acct2026-11"] = testAllowance
	off.store.stored["acct2026-11"] = 2 * testGiB * testNovemberSecs
	if summary, err := off.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 0 || len(off.published) != 0 {
		t.Fatalf("storage charged without overage: %+v, %v, %+v", summary, err, off.published)
	}
}
