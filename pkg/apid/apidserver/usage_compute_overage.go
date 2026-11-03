package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// usageComputeView is a month's compute allowance and billed overage as the
// usage pages show them: the figures the provider is billed and the spend cap
// reads, not usage above today's plan allowance (ADR-20261101113742).
type usageComputeView struct {
	IncludedGBHours int64
	OverageGBHours  float64
	OverageCents    int64
}

// monthComputeView reads acct's compute allowance and billed overage for the
// month containing month, given usage, the month's usage so far.
func (s *server) monthComputeView(ctx context.Context, acct state.Account, month time.Time, usage int64) (usageComputeView, error) {
	compute, err := billing.ComputeOverageForMonth(ctx, s.store, acct, month, time.Now(), usage)
	if err != nil {
		return usageComputeView{}, err
	}
	return usageComputeView{
		IncludedGBHours: compute.IncludedMBSeconds / api.SecondsPerGBHour,
		OverageGBHours:  float64(compute.BilledMBSeconds) / float64(api.SecondsPerGBHour),
		OverageCents:    api.OverageCentsForBillableMBSeconds(compute.BilledMBSeconds),
	}, nil
}
