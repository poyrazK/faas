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
	// Hobby's whole overage room costs $5.
	if _, millicents := OverageCharge(2*testAllowance, testAllowance); millicents != 500*api.MillicentsPerCent {
		t.Fatalf("Hobby overage room = %d millicents, want $5", millicents)
	}
}

type overageUsageStore struct {
	*MemoryStore
	used map[string]int64
}

func (s *overageUsageStore) UsageSnapshot(_ context.Context, subjectID string, at time.Time) (UsageSnapshot, error) {
	return UsageSnapshot{PeriodStart: monthStart(at), ComputeUnitSeconds: s.used[subjectID+monthStart(at).Format("2006-01")]}, nil
}

type overageFixture struct {
	store     *overageUsageStore
	now       time.Time
	limits    CapLimits
	published []OverageChargeStep
	fail      error
}

func newOverageFixture(t *testing.T, now time.Time) *overageFixture {
	t.Helper()
	return &overageFixture{store: &overageUsageStore{MemoryStore: NewMemoryStore(), used: map[string]int64{}}, now: now,
		limits: CapLimits{Allowance: testAllowance, Ceiling: 2 * testAllowance, Overage: true}}
}

func (f *overageFixture) biller(t *testing.T, live bool, from time.Time) *OverageBiller {
	t.Helper()
	options := OverageBillerOptions{Provider: "polar", EffectiveFrom: from, Now: func() time.Time { return f.now },
		Allowance: func(_ context.Context, subject string) (CapLimits, error) {
			if subject == "gone" {
				return CapLimits{}, ErrNotFound
			}
			return f.limits, nil
		}}
	if live {
		options.Publish = func(_ context.Context, step OverageChargeStep) error {
			if f.fail != nil {
				return f.fail
			}
			f.published = append(f.published, step)
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
	if summary, err := biller.Deliver(ctx); err != nil || summary.Delivered != 1 || summary.Millicents != 40_000 {
		t.Fatalf("first pass = %+v, %v", summary, err)
	}
	if len(f.published) != 1 || f.published[0].DeliveredMillicents != 0 || f.published[0].TargetMillicents != 40_000 || f.published[0].BilledUnitSeconds != 2*3600 {
		t.Fatalf("published %+v", f.published)
	}
	if summary, err := biller.Deliver(ctx); err != nil || summary.Delivered != 0 || len(f.published) != 1 {
		t.Fatalf("unchanged usage charged again: %+v, %v", summary, err)
	}
	f.store.used["acct2026-11"] = testAllowance + 3*3600
	if _, err := biller.Deliver(ctx); err != nil || len(f.published) != 2 || f.published[1].DeliveredMillicents != 40_000 || f.published[1].TargetMillicents != 60_000 {
		t.Fatalf("second step %+v, %v", f.published, err)
	}
	// Usage past the ceiling is charged only up to it.
	f.store.used["acct2026-11"] = 10 * testAllowance
	if _, err := biller.Deliver(ctx); err != nil || f.published[2].TargetMillicents != 25*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("ceiling step %+v, %v", f.published, err)
	}
	delivery, err := f.store.OverageDelivered(ctx, "polar", "acct", november)
	if err != nil || !delivery.Live || delivery.Millicents != 500_000 || delivery.BilledUnitSeconds != testAllowance {
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
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 1 || f.published[0].TargetMillicents != 20_000 {
		t.Fatalf("retry %+v, %v", f.published, err)
	}

	shadow := newOverageFixture(t, november.Add(10*24*time.Hour))
	shadow.reachAllowance(t, "acct", november, true)
	shadow.store.used["acct2026-11"] = testAllowance + 3600
	summary, err := shadow.biller(t, false, november).Deliver(ctx)
	if err != nil || summary.Shadowed != 1 || len(shadow.published) != 0 {
		t.Fatalf("shadow = %+v, %v, published %v", summary, err, shadow.published)
	}
	if delivery, _ := shadow.store.OverageDelivered(ctx, "polar", "acct", november); delivery.Live || delivery.Millicents != 20_000 {
		t.Fatalf("shadow recorded %+v", delivery)
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
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, 0); err != nil || !recorded {
		t.Fatalf("first = %v, %v", recorded, err)
	}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, 0); err != nil || recorded {
		t.Fatalf("a replica that lost the race recorded: %v, %v", recorded, err)
	}
	delivery.Millicents = 10_000
	if _, err := store.RecordOverageDelivery(ctx, delivery, 20_000); err == nil {
		t.Fatal("a lower charge was accepted")
	}
	delivery.PeriodStart = november.Add(time.Hour)
	delivery.Millicents = 30_000
	if _, err := store.RecordOverageDelivery(ctx, delivery, 20_000); err == nil {
		t.Fatal("a mid-month period was accepted")
	}
}
