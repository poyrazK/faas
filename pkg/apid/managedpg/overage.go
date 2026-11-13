package managedpg

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// PlanHistory reads the activations meterd records for every billing subject,
// including organizations and accounts without app compute.
type PlanHistory interface {
	ListFinancialPriceSnapshots(context.Context, string, time.Time) ([]state.FinancialPriceSnapshot, error)
}

// OverageAllowances keeps a month's included PostgreSQL compute and storage
// when the subject downgrades. Like the storage pool, each month's allowance
// is the largest recorded plan allowance. Today's plan also applies in the
// month in progress, before meterd has recorded its latest activation.
func OverageAllowances(current managedpostgres.CapCeiling, history PlanHistory, now func() time.Time) managedpostgres.OverageAllowance {
	return func(ctx context.Context, subject string, month time.Time) (managedpostgres.CapLimits, error) {
		limits, err := current(ctx, subject)
		if err != nil {
			return managedpostgres.CapLimits{}, err
		}
		snapshots, err := history.ListFinancialPriceSnapshots(ctx, subject, month)
		if err != nil {
			return limits, fmt.Errorf("managed postgres billing plan history: %w", err)
		}
		at := now().UTC()
		closed := !at.Before(month.AddDate(0, 1, 0))
		through := at
		if closed {
			through = month.AddDate(0, 1, 0)
		}
		var compute, storage int64
		found := false
		for _, snapshot := range snapshots {
			if snapshot.Price.Meter != "compute" || !snapshot.EffectiveFrom.Before(through) {
				continue
			}
			plan, ok := api.ManagedPostgresLimitsFor(snapshot.Plan)
			if !ok {
				return limits, fmt.Errorf("managed postgres billing plan %q: %w", snapshot.Plan, managedpostgres.ErrUnavailable)
			}
			found = true
			compute = max(compute, plan.MonthlyComputeUnitSeconds)
			storage = max(storage, plan.StorageLimitBytes)
		}
		if closed && !found {
			// Guessing from the current plan could charge usage already
			// included last month. Defer until its terms are available.
			return limits, fmt.Errorf("managed postgres billing plan history missing for %s: %w", month.Format("2006-01"), managedpostgres.ErrUnavailable)
		}
		if !closed {
			compute = max(compute, limits.Allowance)
			storage = max(storage, limits.StorageAllowance)
		}
		limits.Allowance, limits.StorageAllowance = compute, storage
		return limits, nil
	}
}
