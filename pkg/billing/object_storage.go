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

// ObjectStorageLineItemPolicy is the explicit commercial rollout boundary for
// object-storage charges. EffectiveFrom must be a UTC month boundary. Off is
// represented by a sink returning ok=false.
type ObjectStorageLineItemPolicy struct {
	Provider      string
	Mode          MeterDeliveryMode
	EffectiveFrom time.Time
}

func (p ObjectStorageLineItemPolicy) valid() bool {
	_, utcOffset := p.EffectiveFrom.Zone()
	return p.Provider != "" &&
		(p.Mode == MeterDeliveryShadow || p.Mode == MeterDeliveryLive) &&
		!p.EffectiveFrom.IsZero() && utcOffset == 0 &&
		p.EffectiveFrom.Equal(state.ObjectStoragePeriod(p.EffectiveFrom))
}

// ObjectStorageLineItemSink is the provider adapter seam for object-storage
// charges, delivered in steps during the month and finished at month close
// (ADR-20261027093220). Implementations must expose an explicit rollout
// policy, bill each step's TargetMillicents − DeliveredMillicents, key their
// external idempotency on the account, the month and TargetMillicents, and
// translate millicents into the provider's native currency unit. Steps are
// already held under the account's overage cap (ADR-1320). Keeping this
// optional interface separate from Provider avoids forcing every billing
// integration to support object-storage charges at once.
type ObjectStorageLineItemSink interface {
	ObjectStorageLineItemPolicy() (ObjectStorageLineItemPolicy, bool)
	PublishObjectStorageCharge(ctx context.Context, step ObjectStorageChargeStep) error
}

// ObjectStorageAccountSource is the narrow account-walk seam used by the
// month-close runner. It is kept separate from state.Store so billing tests
// and future billing daemons do not need to depend on the full control-plane
// store interface.
type ObjectStorageAccountSource interface {
	ListAllAccounts(context.Context) ([]state.Account, error)
}

// FinalizeObjectStoragePeriods closes the completed UTC month for every
// account that had object-storage activity. With a sink, a last step brings
// the charge delivered during the month up to the record's total under the
// cap, and a receipt records the month's cumulative charge
// (ADR-20261027093220). The ledger write happens before publication; a retry
// reuses the same record, and the step's idempotency key lets the sink
// deduplicate an ambiguous provider response.
//
// Accounts without buckets, reports, or authorizations are skipped so a
// deployment does not manufacture zero-value invoice rows for every compute
// account. Missing provider reports for an account that did use storage still
// fail closed through FinalizeObjectStoragePeriod.
func FinalizeObjectStoragePeriods(ctx context.Context, accounts ObjectStorageAccountSource, store state.ObjectStorageBillingStore, pricing api.ObjectStoragePricing, periodStart, now time.Time, sink ObjectStorageLineItemSink) ([]state.ObjectStorageBillingRecord, error) {
	if accounts == nil || store == nil || !pricing.Valid() {
		return nil, errors.New("billing: invalid object storage period runner")
	}
	periodStart = state.ObjectStoragePeriod(periodStart)
	now = now.UTC()
	if !periodStart.Before(state.ObjectStoragePeriod(now)) {
		return nil, state.ErrObjectBillingOpen
	}
	run, deliver, err := newObjectStorageChargeRun(store, sink, now)
	if err != nil {
		return nil, err
	}
	all, err := accounts.ListAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	var finalized []state.ObjectStorageBillingRecord
	var failures []error
	for _, account := range all {
		if err := ctx.Err(); err != nil {
			return finalized, err
		}
		snapshot, err := store.ObjectUsageForPeriod(ctx, account.ID, periodStart)
		if err != nil {
			failures = append(failures, fmt.Errorf("account %s: read object usage: %w", account.ID, err))
			continue
		}
		if len(snapshot.Buckets) == 0 && len(snapshot.Reports) == 0 && snapshot.Authorizations == 0 {
			continue
		}
		allowance, err := objectStorageMonthAllowance(ctx, store, account, periodStart)
		if err != nil {
			failures = append(failures, fmt.Errorf("account %s: object storage allowance: %w", account.ID, err))
			continue
		}
		record, err := FinalizeObjectStoragePeriod(ctx, store, account.ID, pricing, allowance, periodStart, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("account %s: finalize object usage: %w", account.ID, err))
			continue
		}
		if deliver {
			if _, err := store.GetObjectStorageBillingDelivery(ctx, run.policy.Provider, record.ID); err == nil {
				finalized = append(finalized, record)
				continue
			} else if !errors.Is(err, state.ErrNotFound) {
				failures = append(failures, fmt.Errorf("account %s: read object billing delivery: %w", account.ID, err))
				continue
			}
			// Shadow records what live would bill, so both stay under the
			// overage cap (ADR-1320).
			delivery, err := run.closeMonth(ctx, account, record)
			if err != nil {
				failures = append(failures, fmt.Errorf("account %s: deliver object usage: %w", account.ID, err))
				continue
			}
			if _, err := store.RecordObjectStorageBillingDelivery(ctx, delivery); err != nil {
				failures = append(failures, fmt.Errorf("account %s: record object billing delivery: %w", account.ID, err))
				continue
			}
		}
		finalized = append(finalized, record)
	}
	return finalized, errors.Join(failures...)
}

// objectStorageMonthAllowance follows the period's plan history like compute
// (ADR-1162): the largest allowance, per meter, among the plans whose
// activations were recorded in the month. Without recorded activations the
// account's current plan applies.
func objectStorageMonthAllowance(ctx context.Context, store state.ObjectStorageBillingStore, account state.Account, periodStart time.Time) (api.ObjectStorageAllowance, error) {
	plans := []api.Plan{}
	if history, ok := store.(state.FinancialStore); ok {
		snapshots, err := history.ListFinancialPriceSnapshots(ctx, account.ID, periodStart)
		if err != nil {
			return api.ObjectStorageAllowance{}, err
		}
		for _, snapshot := range snapshots {
			if snapshot.Price.Meter == "compute" && snapshot.Plan.Valid() {
				plans = append(plans, snapshot.Plan)
			}
		}
	}
	if len(plans) == 0 {
		plans = append(plans, account.Plan)
	}
	return largestObjectStorageAllowance(plans, planObjectStorageAllowance)
}

func planObjectStorageAllowance(plan api.Plan) (api.ObjectStorageAllowance, bool) {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return api.ObjectStorageAllowance{}, false
	}
	return limits.ObjectStorage.Allowance(), true
}

// largestObjectStorageAllowance takes the per-meter maximum across plans, so
// a mid-month downgrade never shrinks the month's allowance.
func largestObjectStorageAllowance(plans []api.Plan, lookup func(api.Plan) (api.ObjectStorageAllowance, bool)) (api.ObjectStorageAllowance, error) {
	var out api.ObjectStorageAllowance
	for _, plan := range plans {
		a, ok := lookup(plan)
		if !ok {
			return api.ObjectStorageAllowance{}, fmt.Errorf("unknown plan %q", plan)
		}
		out.StoredByteHours = max(out.StoredByteHours, a.StoredByteHours)
		out.Requests = max(out.Requests, a.Requests)
		out.EgressBytes = max(out.EgressBytes, a.EgressBytes)
	}
	return out, nil
}

// FinalizeObjectStoragePeriod closes one completed UTC month and records the
// exact usage and rate card used to calculate its customer charge. The state
// store makes retries idempotent on (account, period); a changed rate card or
// usage snapshot cannot silently rewrite a finalized period.
//
// This function intentionally stops at a durable internal ledger. A concrete
// billing provider can consume the returned record through its own adapter
// without coupling object-storage accounting to Stripe, Paddle, Polar, or a
// particular storage backend.
func FinalizeObjectStoragePeriod(ctx context.Context, store state.ObjectStorageBillingStore, accountID string, pricing api.ObjectStoragePricing, allowance api.ObjectStorageAllowance, periodStart, now time.Time) (state.ObjectStorageBillingRecord, error) {
	if store == nil || accountID == "" || !pricing.Valid() || !allowance.Valid() {
		return state.ObjectStorageBillingRecord{}, errors.New("billing: invalid object storage finalization request")
	}
	periodStart = state.ObjectStoragePeriod(periodStart)
	now = now.UTC()
	if !periodStart.Before(state.ObjectStoragePeriod(now)) {
		return state.ObjectStorageBillingRecord{}, state.ErrObjectBillingOpen
	}

	if existing, err := store.GetObjectStorageBillingPeriod(ctx, accountID, periodStart); err == nil {
		return existing, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.ObjectStorageBillingRecord{}, err
	}

	snapshot, err := store.ObjectUsageForPeriod(ctx, accountID, periodStart)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	usage, err := state.SummarizeObjectStorageBillingUsage(snapshot, periodStart)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	charge, err := objectstorage.CalculateCharge(pricing, usage, allowance)
	if err != nil {
		return state.ObjectStorageBillingRecord{}, err
	}
	record := state.ObjectStorageBillingRecord{
		AccountID:                    accountID,
		PeriodStart:                  periodStart,
		PeriodEnd:                    periodStart.AddDate(0, 1, 0),
		Currency:                     charge.Currency,
		StoredByteHours:              usage.StoredByteHours,
		RequestCount:                 usage.RequestCount,
		EgressBytes:                  usage.EgressBytes,
		IncludedStoredByteHours:      allowance.StoredByteHours,
		IncludedRequests:             allowance.Requests,
		IncludedEgressBytes:          allowance.EgressBytes,
		ProviderCostMillicents:       usage.CostMillicents,
		StorageMillicentsPerGiBMonth: pricing.StorageMillicentsPerGiBMonth,
		RequestsMillicentsPerMillion: pricing.RequestsMillicentsPerMillion,
		EgressMillicentsPerGiB:       pricing.EgressMillicentsPerGiB,
		StorageMillicents:            charge.StorageMillicents,
		RequestsMillicents:           charge.RequestsMillicents,
		EgressMillicents:             charge.EgressMillicents,
		TotalMillicents:              charge.TotalMillicents,
		FinalizedAt:                  now,
	}
	return store.RecordObjectStorageBillingPeriod(ctx, record)
}
