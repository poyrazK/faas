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
// month-close object-storage charges. EffectiveFrom must be a UTC month
// boundary. Off is represented by a sink returning ok=false.
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

// ObjectStorageLineItemSink is the provider adapter seam for a finalized
// period. Implementations must expose an explicit rollout policy, use
// record.ID as their external idempotency key, and translate millicents into
// the provider's native currency unit. chargeMillicents is what to bill: the
// record's total held under the account's overage cap (ADR-1320), never more
// than record.TotalMillicents. Keeping this optional interface separate from
// Provider avoids forcing every billing integration to support object-storage
// line items at once.
type ObjectStorageLineItemSink interface {
	ObjectStorageLineItemPolicy() (ObjectStorageLineItemPolicy, bool)
	PublishObjectStorageLineItem(ctx context.Context, record state.ObjectStorageBillingRecord, chargeMillicents int64) error
}

// ObjectStorageAccountSource is the narrow account-walk seam used by the
// month-close runner. It is kept separate from state.Store so billing tests
// and future billing daemons do not need to depend on the full control-plane
// store interface.
type ObjectStorageAccountSource interface {
	ListAllAccounts(context.Context) ([]state.Account, error)
}

// FinalizeObjectStoragePeriods closes the completed UTC month for every
// account that had object-storage activity and optionally publishes each
// immutable record to the configured billing provider. The ledger write
// happens before publication; a later retry therefore reuses the same record
// ID and lets the sink deduplicate an ambiguous provider response.
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
	var policy ObjectStorageLineItemPolicy
	if sink != nil {
		var enabled bool
		policy, enabled = sink.ObjectStorageLineItemPolicy()
		if !enabled {
			sink = nil
		} else if !policy.valid() {
			return nil, errors.New("billing: invalid object storage line item policy")
		}
	}
	// The sink's egress policy prices the live egress that the overage cap
	// reserves before object storage, as meterd prices it (ADR-1320).
	policies, _ := sink.(MeterUsagePolicyProvider)
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
		if sink != nil {
			if _, err := store.GetObjectStorageBillingDelivery(ctx, policy.Provider, record.ID); err == nil {
				finalized = append(finalized, record)
				continue
			} else if !errors.Is(err, state.ErrNotFound) {
				failures = append(failures, fmt.Errorf("account %s: read object billing delivery: %w", account.ID, err))
				continue
			}

			delivery := state.ObjectStorageBillingDelivery{
				Provider:           policy.Provider,
				BillingRecordID:    record.ID,
				AccountID:          record.AccountID,
				PeriodStart:        record.PeriodStart,
				QuantityMillicents: record.TotalMillicents,
				DeliveredAt:        now,
			}
			switch {
			case record.PeriodStart.Before(policy.EffectiveFrom):
				delivery.Mode = state.ObjectStorageDeliveryPreActivation
				delivery.QuantityMillicents = 0
			case !account.Plan.IsPaid():
				delivery.Mode = state.ObjectStorageDeliveryPlanIneligible
				delivery.QuantityMillicents = 0
			default:
				// Shadow records what live would bill, so both stay under the
				// overage cap (ADR-1320).
				charge, err := objectStorageChargeWithinCap(ctx, store, policies, account, record)
				if err != nil {
					failures = append(failures, fmt.Errorf("account %s: object storage spend cap: %w", account.ID, err))
					continue
				}
				delivery.QuantityMillicents = charge
				delivery.Mode = state.ObjectStorageDeliveryShadow
				if policy.Mode == MeterDeliveryLive {
					delivery.Mode = state.ObjectStorageDeliveryLive
					if err := sink.PublishObjectStorageLineItem(ctx, record, charge); err != nil {
						failures = append(failures, fmt.Errorf("account %s: publish object usage: %w", account.ID, err))
						continue
					}
				}
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
