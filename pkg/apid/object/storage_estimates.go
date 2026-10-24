package object

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RecordObjectStorageEstimates records each billing account's live
// object-storage estimate for budgets (ADR-20261020160000). It uses the
// same usage, pricing and current-plan allowance as the usage endpoint. An
// account without buckets records zero usage, so a budget that selects
// object storage stays complete. Usage with unavailable meters is not
// recorded; the previous estimate ages out as stale instead.
func (s *Service) RecordObjectStorageEstimates(ctx context.Context, now time.Time) error {
	registry := s.Host.ObjectStorage()
	if registry == nil || registry.Pricing == nil || registry.Accounting.GatewaySafety() {
		return nil
	}
	store := s.Host.Store()
	usage, ok := store.(state.ObjectStorageAccountingStore)
	estimates, ok2 := store.(state.FinancialObjectStorageEstimateStore)
	if !ok || !ok2 {
		return nil
	}
	accounts, err := state.ListBillingResourceAccounts(ctx, store)
	if err != nil {
		return err
	}
	now = now.UTC()
	period := state.ObjectStoragePeriod(now)
	var failures []error
	for _, acct := range accounts {
		snapshot, err := usage.ObjectUsage(ctx, acct.ResourceOwnerID(), now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		summary := state.SummarizeObjectUsage(snapshot, registry.Accounting, now)
		if !summary.Chargeable() {
			continue
		}
		e := state.FinancialObjectStorageEstimate{AccountID: acct.ID, PeriodStart: period, ObservedAt: now, UsageThrough: objectUsageThrough(snapshot, period, now), Fresh: summary.Fresh, Usage: summary, Pricing: *registry.Pricing, Allowance: api.MustLimitsFor(acct.Plan).ObjectStorage.Allowance()}
		failures = append(failures, estimates.PutFinancialObjectStorageEstimate(ctx, e))
	}
	return errors.Join(failures...)
}

// objectUsageThrough is the oldest provider report the month's usage
// includes, or under gateway metering the oldest live bucket's metered
// instant. With neither, the usage is known only through the period start,
// which never supports a run-rate forecast.
func objectUsageThrough(s state.ObjectUsageSnapshot, period, now time.Time) time.Time {
	through := time.Time{}
	for _, b := range s.Buckets {
		if b.Bucket.State == "deleted" || b.StorageMeteredThrough.IsZero() || b.StorageMeteredThrough.After(now) {
			continue
		}
		if through.IsZero() || b.StorageMeteredThrough.Before(through) {
			through = b.StorageMeteredThrough.UTC()
		}
	}
	for _, r := range s.Reports {
		if !r.PeriodStart.Equal(period) || r.ObservedAt.After(now) {
			continue
		}
		if through.IsZero() || r.ObservedAt.Before(through) {
			through = r.ObservedAt.UTC()
		}
	}
	if through.Before(period) {
		return period
	}
	return through
}
