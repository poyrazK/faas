package managedpostgres

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AllowanceSpan is the allowance a billing subject held until End: its plan's
// monthly compute allowance and storage pool (ADR-20261103131742
// amendment 2).
type AllowanceSpan struct {
	End              time.Time
	Allowance        int64
	StorageAllowance int64
}

// UsageBeforeReader is an optional UsageStore capability: a subject's usage
// in month's windows that start before before.
type UsageBeforeReader interface {
	UsageBefore(ctx context.Context, subjectID string, month, before time.Time) (UsageSnapshot, error)
}

// spanCharge is a month's charge with each span's usage billed against that
// span's allowance, as app compute is billed hour by hour against the
// allowance in force (ADR-20261030093512): after a downgrade, usage the
// larger allowance covered is not billed again; after an upgrade, overage
// already billed stays billed.
type spanCharge struct {
	billedUnitSeconds, billedStorageByteSeconds, millicents int64
	allowance, storageAllowance                             int64
}

// chargeBySpan sums each span's usage above its own allowance, then charges
// at most one more allowance of the plan held now (the last span), as its
// doubled ceiling allows. A span on a plan without an allowance bills
// nothing. total is the month's usage through the last span's end.
func chargeBySpan(ctx context.Context, usage UsageBeforeReader, subjectID string, month time.Time, spans []AllowanceSpan, total UsageSnapshot, storage bool) (spanCharge, error) {
	month = monthStart(month)
	monthSeconds := int64(month.AddDate(0, 1, 0).Sub(month) / time.Second)
	above := func(used, allowance int64) int64 { return max(used-allowance, 0) }
	var out spanCharge
	var computeOver, storageOver int64
	var before UsageSnapshot // the month's usage before the span starts
	for i, span := range spans {
		upTo := total
		if i < len(spans)-1 {
			var err error
			if upTo, err = usage.UsageBefore(ctx, subjectID, month, span.End); err != nil {
				return spanCharge{}, err
			}
		}
		if span.Allowance > 0 {
			computeOver = saturatingAdd(computeOver, max(above(upTo.ComputeUnitSeconds, span.Allowance)-above(before.ComputeUnitSeconds, span.Allowance), 0))
		}
		if storage && span.StorageAllowance > 0 {
			pool := poolByteSeconds(span.StorageAllowance, monthSeconds)
			storageOver = saturatingAdd(storageOver, max(above(upTo.StorageByteSeconds, pool)-above(before.StorageByteSeconds, pool), 0))
		}
		out.allowance, out.storageAllowance = span.Allowance, span.StorageAllowance
		before = upTo
	}
	// OverageCharge and StorageOverageCharge bill usage above an allowance up
	// to the ceiling; the current allowance plus the summed overage is exactly
	// that overage, bounded and priced as they price it.
	out.billedUnitSeconds, out.millicents = OverageCharge(saturatingAdd(computeOver, out.allowance), out.allowance)
	if storage && out.storageAllowance > 0 {
		var millicents int64
		out.billedStorageByteSeconds, millicents = StorageOverageCharge(saturatingAdd(storageOver, poolByteSeconds(out.storageAllowance, monthSeconds)), out.storageAllowance, month)
		out.millicents = saturatingAdd(out.millicents, millicents)
	}
	return out, nil
}

// poolByteSeconds is a storage pool held for a whole month, saturating.
func poolByteSeconds(poolBytes, monthSeconds int64) int64 {
	if poolBytes > 0 && monthSeconds > math.MaxInt64/poolBytes {
		return math.MaxInt64
	}
	return poolBytes * monthSeconds
}

// UsageBefore is the subject's usage in month's windows that start before
// before, waivers applied as UsageSnapshot applies them.
func (s *PostgresStore) UsageBefore(ctx context.Context, subjectID string, month, before time.Time) (UsageSnapshot, error) {
	account, err := postgresUUID(subjectID)
	if err != nil || month.IsZero() {
		return UsageSnapshot{}, ErrInvalid
	}
	start := monthStart(month)
	end := monthStart(start.AddDate(0, 1, 0))
	if before.Before(end) {
		end = before
	}
	totals, err := sqlc.New().ManagedPostgresUsageForSubject(ctx, s.catalog, sqlc.ManagedPostgresUsageForSubjectParams{
		SubjectID: account, PeriodStart: databaseNullableTime(start), PeriodEnd: databaseNullableTime(end)})
	if err != nil {
		return UsageSnapshot{}, mapPostgresError(err)
	}
	return UsageSnapshot{PeriodStart: start, ComputeUnitSeconds: totals.ComputeUnitSeconds, StorageByteSeconds: totals.StorageByteSeconds,
		HistoryByteSeconds: totals.HistoryByteSeconds, EgressBytes: totals.EgressBytes, CostMillicents: totals.CostMillicents}, nil
}

// UsageBefore is the subject's usage in month's windows that start before
// before, waivers applied as UsageSnapshot applies them.
func (s *MemoryStore) UsageBefore(_ context.Context, subjectID string, month, before time.Time) (UsageSnapshot, error) {
	if subjectID == "" || month.IsZero() {
		return UsageSnapshot{}, ErrInvalid
	}
	start := monthStart(month)
	end := monthStart(start.AddDate(0, 1, 0))
	if before.Before(end) {
		end = before
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := UsageSnapshot{PeriodStart: start}
	for _, record := range s.usage {
		if s.billingSubject(s.databases[record.DatabaseID]) != subjectID || record.WindowFrom.Before(start) || !record.WindowFrom.Before(end) ||
			s.usageWaivedLocked(record) {
			continue
		}
		var err error
		switch record.Meter {
		case MeterComputeUnitSeconds:
			snapshot.ComputeUnitSeconds, err = addUsage(snapshot.ComputeUnitSeconds, record.Quantity)
		case MeterStorageByteSeconds:
			snapshot.StorageByteSeconds, err = addUsage(snapshot.StorageByteSeconds, record.Quantity)
		}
		if err != nil {
			return UsageSnapshot{}, err
		}
	}
	return snapshot, nil
}
