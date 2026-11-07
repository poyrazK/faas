package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOverageBillerUsesEachMonthsAllowancesAfterDowngrade(t *testing.T) {
	for _, meter := range []string{"storage", "compute"} {
		t.Run(meter, func(t *testing.T) {
			ctx := context.Background()
			october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			november := october.AddDate(0, 1, 0)
			f := newOverageFixture(t, november.Add(24*time.Hour))
			f.limits.StorageAllowance = testGiB
			scale, _ := api.ManagedPostgresLimitsFor(api.PlanScale)
			f.monthly = map[string]CapLimits{"2026-10": {Allowance: scale.MonthlyComputeUnitSeconds, StorageAllowance: scale.StorageLimitBytes, Overage: true}}
			if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
				t.Fatal(err)
			}
			if meter == "storage" {
				f.store.stored["acct2026-10"] = 5 * testGiB * int64(november.Sub(october)/time.Second)
				f.store.stored["acct2026-11"] = 2 * testGiB * testNovemberSecs
			} else {
				f.store.used["acct2026-10"] = 50 * 3600
				f.store.used["acct2026-11"] = testAllowance + 3600
			}
			want := int64(api.ManagedPostgresStorageOverageMillicentsPerGiBMonth)
			if meter == "compute" {
				want = api.ManagedPostgresOverageMillicentsPerComputeUnitHour
			}
			if summary, err := f.biller(t, true, october).Deliver(ctx); err != nil || summary.Delivered != 1 ||
				len(f.published) != 1 || !f.published[0].PeriodStart.Equal(november) || f.published[0].TargetMillicents != want {
				t.Fatalf("billed the wrong month's %s allowance: summary=%+v steps=%+v err=%v", meter, summary, f.published, err)
			}
		})
	}
}

func TestOverageBillerReplaysReservedStepWithoutMonthlyHistory(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.Add(24*time.Hour))
	if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
		t.Fatal(err)
	}
	step := OveragePendingStep{Provider: "polar", Live: true, Step: OverageChargeStep{SubjectID: "acct", PeriodStart: month,
		At: f.now, UsedUnitSeconds: testAllowance + 3600, AllowanceSeconds: testAllowance, BilledUnitSeconds: 3600,
		TargetMillicents: api.ManagedPostgresOverageMillicentsPerComputeUnitHour}}
	if reserved, err := f.store.ReserveOverage(ctx, step); err != nil || !reserved {
		t.Fatalf("reserve = %v, %v", reserved, err)
	}
	biller := f.biller(t, true, month)
	biller.options.Allowance = func(context.Context, string, time.Time) (CapLimits, error) { return CapLimits{}, ErrUnavailable }
	if summary, err := biller.Deliver(ctx); err != nil || summary.Delivered != 1 || len(f.published) != 1 || f.published[0] != step.Step {
		t.Fatalf("monthly history outage changed an immutable pending step: summary=%+v steps=%+v err=%v", summary, f.published, err)
	}
}

func TestOverageBillerMissingMonthlyHistoryDefersOnlyUsedMonths(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.Add(24*time.Hour))
	if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
		t.Fatal(err)
	}
	biller := f.biller(t, true, month.AddDate(0, -1, 0))
	biller.options.Allowance = func(context.Context, string, time.Time) (CapLimits, error) { return CapLimits{}, ErrUnavailable }
	if summary, err := biller.Deliver(ctx); err != nil || summary.Failed != 0 || summary.Delivered != 0 {
		t.Fatalf("unused months required plan history: summary=%+v err=%v", summary, err)
	}
	f.store.used["acct2026-10"] = 50 * 3600
	if summary, err := biller.Deliver(ctx); !errors.Is(err, ErrUnavailable) || summary.Failed != 1 || len(f.published) != 0 {
		t.Fatalf("used month was priced without its history: summary=%+v steps=%+v err=%v", summary, f.published, err)
	}
}

func TestOverageBillerOffSubjectNeedsNoMonthlyTerms(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.Add(24*time.Hour))
	f.reachAllowance(t, "acct", month, false)
	f.store.used["acct2026-11"] = 50 * 3600
	biller := f.biller(t, true, month)
	biller.options.Allowance = func(context.Context, string, time.Time) (CapLimits, error) { return CapLimits{}, ErrUnavailable }
	if summary, err := biller.Deliver(ctx); err != nil || summary.Failed != 0 || len(f.published) != 0 {
		t.Fatalf("an ineligible subject required billing terms: summary=%+v steps=%+v err=%v", summary, f.published, err)
	}
}
