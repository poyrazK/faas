package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Object storage is billed in daily steps inside its UTC month, and month
// close finishes the month (ADR-20261027093220). A month billed only at close
// arrived after a churned customer's subscription had ended, so their last
// month of storage was never invoiced (Hunt 19 H19-1).

// ObjectStorageChargeStep raises an account's delivered object-storage charge
// for one UTC month from DeliveredMillicents to TargetMillicents. Steps are
// cumulative: a sink bills TargetMillicents − DeliveredMillicents and keys
// its idempotency on the account, the month and TargetMillicents. The
// month-close step carries the finalized record.
type ObjectStorageChargeStep struct {
	AccountID           string
	PeriodStart         time.Time
	DeliveredMillicents int64
	TargetMillicents    int64
	At                  time.Time
	Record              *state.ObjectStorageBillingRecord
}

// objectStorageChargeRun delivers steps under one sink policy.
type objectStorageChargeRun struct {
	store    state.ObjectStorageBillingStore
	progress state.ObjectStorageBillingProgressStore
	sink     ObjectStorageLineItemSink
	policy   ObjectStorageLineItemPolicy
	// policies prices the live egress that the overage cap reserves before
	// object storage, as meterd prices it (ADR-1320).
	policies MeterUsagePolicyProvider
	now      time.Time
}

// newObjectStorageChargeRun returns ok=false when no sink is enabled.
func newObjectStorageChargeRun(store state.ObjectStorageBillingStore, sink ObjectStorageLineItemSink, now time.Time) (objectStorageChargeRun, bool, error) {
	if sink == nil {
		return objectStorageChargeRun{}, false, nil
	}
	policy, enabled := sink.ObjectStorageLineItemPolicy()
	if !enabled {
		return objectStorageChargeRun{}, false, nil
	}
	if !policy.valid() {
		return objectStorageChargeRun{}, false, errors.New("billing: invalid object storage line item policy")
	}
	progress, ok := store.(state.ObjectStorageBillingProgressStore)
	if !ok {
		return objectStorageChargeRun{}, false, errors.New("billing: object storage progress store is unavailable")
	}
	policies, _ := sink.(MeterUsagePolicyProvider)
	return objectStorageChargeRun{store: store, progress: progress, sink: sink, policy: policy, policies: policies, now: now.UTC()}, true, nil
}

func (r objectStorageChargeRun) mode() string {
	if r.policy.Mode == MeterDeliveryLive {
		return state.ObjectStorageDeliveryLive
	}
	return state.ObjectStorageDeliveryShadow
}

// DeliverObjectStorageProgress steps every paid account whose last step is at
// least api.ObjectStorageProgressInterval old toward its month-to-date
// object-storage charge, and returns how many it stepped. Shadow steps record
// what live would bill without publishing.
func DeliverObjectStorageProgress(ctx context.Context, accounts ObjectStorageAccountSource, store state.ObjectStorageBillingStore, pricing api.ObjectStoragePricing, now time.Time, sink ObjectStorageLineItemSink) (int, error) {
	if accounts == nil || store == nil || !pricing.Valid() {
		return 0, errors.New("billing: invalid object storage progress runner")
	}
	run, enabled, err := newObjectStorageChargeRun(store, sink, now)
	if err != nil || !enabled {
		return 0, err
	}
	period := state.ObjectStoragePeriod(run.now)
	if period.Before(run.policy.EffectiveFrom) {
		return 0, nil
	}
	all, err := accounts.ListAllAccounts(ctx)
	if err != nil {
		return 0, err
	}
	stepped := 0
	var failures []error
	for _, account := range all {
		if err := ctx.Err(); err != nil {
			return stepped, err
		}
		if !account.Plan.IsPaid() {
			continue
		}
		if p, err := run.progress.GetObjectStorageBillingProgress(ctx, run.policy.Provider, account.ID, period, run.mode()); err == nil && !objectStorageProgressDue(p, run.now) {
			continue
		} else if err != nil && !errors.Is(err, state.ErrNotFound) {
			failures = append(failures, fmt.Errorf("account %s: read object storage progress: %w", account.ID, err))
			continue
		}
		charge, active, err := objectStorageChargeToDate(ctx, store, pricing, account, period)
		if err != nil {
			failures = append(failures, fmt.Errorf("account %s: %w", account.ID, err))
			continue
		}
		if !active {
			continue
		}
		if _, err := run.step(ctx, account, period, run.now, charge, nil); err != nil {
			failures = append(failures, fmt.Errorf("account %s: %w", account.ID, err))
			continue
		}
		stepped++
	}
	return stepped, errors.Join(failures...)
}

// objectStorageProgressDue reports a month whose last step is an interval
// old, or whose reservation a failed step left behind for a retry interval.
func objectStorageProgressDue(p state.ObjectStorageBillingProgress, now time.Time) bool {
	age := now.Sub(p.UpdatedAt)
	return age >= api.ObjectStorageProgressInterval || p.PendingMillicents > 0 && age >= api.ObjectStorageProgressRetry
}

// objectStorageChargeToDate prices the usage accrued so far this month against
// the month's allowance. Usage only grows, so the charge never exceeds the
// month's final charge unless the allowance grows later. active=false means
// the account has no object-storage activity this month.
func objectStorageChargeToDate(ctx context.Context, store state.ObjectStorageBillingStore, pricing api.ObjectStoragePricing, account state.Account, period time.Time) (int64, bool, error) {
	snapshot, err := store.ObjectUsageForPeriod(ctx, account.ID, period)
	if err != nil {
		return 0, false, fmt.Errorf("read object usage: %w", err)
	}
	if len(snapshot.Buckets) == 0 && len(snapshot.Reports) == 0 && snapshot.Authorizations == 0 {
		return 0, false, nil
	}
	usage, err := state.SummarizeObjectStorageUsageToDate(snapshot, period)
	if err != nil {
		return 0, false, fmt.Errorf("summarize object usage: %w", err)
	}
	allowance, err := objectStorageMonthAllowance(ctx, store, account, period)
	if err != nil {
		return 0, false, fmt.Errorf("object storage allowance: %w", err)
	}
	charge, err := objectstorage.CalculateCharge(pricing, usage, allowance)
	if err != nil {
		return 0, false, fmt.Errorf("price object usage: %w", err)
	}
	return charge.TotalMillicents, true, nil
}

// closeMonth finishes record's month with a last step to its total under the
// cap and returns the month's receipt. An account no longer on a paid plan
// gets no new charge: a step already fixed is finished, and the receipt keeps
// what was billed while it was paid.
func (r objectStorageChargeRun) closeMonth(ctx context.Context, account state.Account, record state.ObjectStorageBillingRecord) (state.ObjectStorageBillingDelivery, error) {
	delivery := state.ObjectStorageBillingDelivery{
		Provider:        r.policy.Provider,
		BillingRecordID: record.ID,
		AccountID:       record.AccountID,
		PeriodStart:     record.PeriodStart,
		Mode:            r.mode(),
		DeliveredAt:     r.now,
	}
	if record.PeriodStart.Before(r.policy.EffectiveFrom) {
		delivery.Mode = state.ObjectStorageDeliveryPreActivation
		return delivery, nil
	}
	target := record.TotalMillicents
	if !account.Plan.IsPaid() {
		target = 0
	}
	p, err := r.step(ctx, account, record.PeriodStart, record.PeriodEnd, target, &record)
	if err != nil {
		return state.ObjectStorageBillingDelivery{}, err
	}
	delivery.QuantityMillicents = p.DeliveredMillicents
	if p.DeliveredMillicents == 0 && !account.Plan.IsPaid() {
		delivery.Mode = state.ObjectStorageDeliveryPlanIneligible
	}
	return delivery, nil
}

// step raises the account's delivered charge for the month starting at start
// toward chargeToDate, held under its overage cap over [start, end). Each
// write is a compare-and-set, so a concurrent apid's step makes this one
// fail with state.ErrConflict and leaves the month to the winner.
func (r objectStorageChargeRun) step(ctx context.Context, account state.Account, start, end time.Time, chargeToDate int64, record *state.ObjectStorageBillingRecord) (state.ObjectStorageBillingProgress, error) {
	p, err := r.progress.GetObjectStorageBillingProgress(ctx, r.policy.Provider, account.ID, start, r.mode())
	if errors.Is(err, state.ErrNotFound) {
		p = state.ObjectStorageBillingProgress{Provider: r.policy.Provider, AccountID: account.ID, PeriodStart: state.ObjectStoragePeriod(start), Mode: r.mode()}
	} else if err != nil {
		return p, fmt.Errorf("read object storage progress: %w", err)
	}
	// An organization that adopted its members' buckets mid-month owes only
	// what they were not already charged for the month.
	chargeToDate = max(chargeToDate-p.CreditedMillicents, 0)
	if p.PendingFixed {
		// The step's delta is final and may already be published.
		if p, err = r.complete(ctx, p, record); err != nil {
			return p, err
		}
	}
	if chargeToDate <= p.DeliveredMillicents {
		if p.PendingMillicents > 0 {
			return r.save(ctx, p, 0, false)
		}
		return p, nil
	}
	// Reserve the most this step can deliver before reading compute and
	// egress. meterd reserves it in turn, so either it sees the reservation
	// or the read below includes every hour it billed without seeing it.
	reserved, err := r.save(ctx, p, chargeToDate-p.DeliveredMillicents, false)
	if err != nil {
		return p, err
	}
	target, err := objectStorageChargeWithinCap(ctx, r.store, r.policies, account, start, end, chargeToDate)
	if err != nil {
		if released, releaseErr := r.save(ctx, reserved, 0, false); releaseErr == nil {
			reserved = released
		}
		return reserved, fmt.Errorf("object storage spend cap: %w", err)
	}
	if target <= reserved.DeliveredMillicents {
		return r.save(ctx, reserved, 0, false)
	}
	fixed, err := r.save(ctx, reserved, target-reserved.DeliveredMillicents, true)
	if err != nil {
		return reserved, err
	}
	return r.complete(ctx, fixed, record)
}

// complete publishes a fixed step when live and adds it to delivered.
func (r objectStorageChargeRun) complete(ctx context.Context, p state.ObjectStorageBillingProgress, record *state.ObjectStorageBillingRecord) (state.ObjectStorageBillingProgress, error) {
	if p.Mode == state.ObjectStorageDeliveryLive {
		step := ObjectStorageChargeStep{
			AccountID:           p.AccountID,
			PeriodStart:         p.PeriodStart,
			DeliveredMillicents: p.DeliveredMillicents,
			TargetMillicents:    p.DeliveredMillicents + p.PendingMillicents,
			At:                  r.now,
			Record:              record,
		}
		if err := r.sink.PublishObjectStorageCharge(ctx, step); err != nil {
			return p, fmt.Errorf("publish object storage charge: %w", err)
		}
	}
	p.DeliveredMillicents += p.PendingMillicents
	return r.save(ctx, p, 0, false)
}

func (r objectStorageChargeRun) save(ctx context.Context, p state.ObjectStorageBillingProgress, pending int64, fixed bool) (state.ObjectStorageBillingProgress, error) {
	p.PendingMillicents, p.PendingFixed = pending, fixed
	saved, err := r.progress.SaveObjectStorageBillingProgress(ctx, p)
	if err != nil {
		return p, fmt.Errorf("save object storage progress: %w", err)
	}
	return saved, nil
}
