package managedpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type overagePlanHistory struct {
	snapshots []state.FinancialPriceSnapshot
	err       error
	subject   string
	month     time.Time
}

func (s *overagePlanHistory) ListFinancialPriceSnapshots(_ context.Context, subject string, month time.Time) ([]state.FinancialPriceSnapshot, error) {
	s.subject, s.month = subject, month
	return s.snapshots, s.err
}

func TestOverageAllowancesFollowTheBilledMonthsPlanHistory(t *testing.T) {
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	november := october.AddDate(0, 1, 0)
	grant := func(plan api.Plan, at time.Time) state.FinancialPriceSnapshot {
		return state.FinancialPriceSnapshot{Plan: plan, EffectiveFrom: at, Price: financial.Price{Meter: "compute"}}
	}
	for _, tc := range []struct {
		name    string
		month   time.Time
		current api.Plan
		history []state.FinancialPriceSnapshot
		readErr error
		want    api.Plan
		wantErr error
	}{
		{"closed month after downgrade", october, api.PlanHobby, []state.FinancialPriceSnapshot{grant(api.PlanScale, october)}, nil, api.PlanScale, nil},
		{"closed month after upgrade", october, api.PlanScale, []state.FinancialPriceSnapshot{grant(api.PlanHobby, october)}, nil, api.PlanHobby, nil},
		{"current month downgrade", november, api.PlanHobby, []state.FinancialPriceSnapshot{grant(api.PlanScale, november)}, nil, api.PlanScale, nil},
		{"current month upgrade before metering", november, api.PlanScale, []state.FinancialPriceSnapshot{grant(api.PlanHobby, november)}, nil, api.PlanScale, nil},
		{"largest allowance in the billed month", october, api.PlanHobby, []state.FinancialPriceSnapshot{grant(api.PlanHobby, october), grant(api.PlanScale, october.Add(24*time.Hour)), grant(api.PlanPro, october.Add(48*time.Hour))}, nil, api.PlanScale, nil},
		{"current month without history", november, api.PlanHobby, nil, nil, api.PlanHobby, nil},
		{"closed month without history", october, api.PlanHobby, nil, nil, "", managedpostgres.ErrUnavailable},
		{"unrelated meter does not establish the plan", october, api.PlanHobby, []state.FinancialPriceSnapshot{{Plan: api.PlanScale, EffectiveFrom: october, Price: financial.Price{Meter: "egress"}}}, nil, "", managedpostgres.ErrUnavailable},
		{"activation at month end belongs to the next month", october, api.PlanScale, []state.FinancialPriceSnapshot{grant(api.PlanHobby, october), grant(api.PlanScale, november)}, nil, api.PlanHobby, nil},
		{"future activation is not applied", november, api.PlanHobby, []state.FinancialPriceSnapshot{grant(api.PlanScale, november.Add(72*time.Hour))}, nil, api.PlanHobby, nil},
		{"history outage", october, api.PlanHobby, nil, managedpostgres.ErrUnavailable, "", managedpostgres.ErrUnavailable},
		{"invalid recorded plan", october, api.PlanHobby, []state.FinancialPriceSnapshot{grant("unknown", october)}, nil, "", managedpostgres.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := &overagePlanHistory{snapshots: tc.history, err: tc.readErr}
			current, _ := api.ManagedPostgresLimitsFor(tc.current)
			lookup := OverageAllowances(func(context.Context, string) (managedpostgres.CapLimits, error) {
				return managedpostgres.CapLimits{Allowance: current.MonthlyComputeUnitSeconds, StorageAllowance: current.StorageLimitBytes, Overage: true}, nil
			}, history, func() time.Time { return november.Add(24 * time.Hour) })
			limits, err := lookup(context.Background(), "org-or-account", tc.month)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("lookup error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			want, _ := api.ManagedPostgresLimitsFor(tc.want)
			if limits.Allowance != want.MonthlyComputeUnitSeconds || limits.StorageAllowance != want.StorageLimitBytes || !limits.Overage ||
				history.subject != "org-or-account" || !history.month.Equal(tc.month) {
				t.Fatalf("wrong month's allowances: %+v, want plan %s", limits, tc.want)
			}
		})
	}
}

func TestOverageAllowancesPreserveDeletedSubjectCheck(t *testing.T) {
	lookup := OverageAllowances(func(context.Context, string) (managedpostgres.CapLimits, error) {
		return managedpostgres.CapLimits{}, managedpostgres.ErrNotFound
	}, nil, time.Now)
	if _, err := lookup(context.Background(), "deleted", time.Now()); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatal(err)
	}
}
