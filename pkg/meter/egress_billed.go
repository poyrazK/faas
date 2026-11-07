package meter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// The egress a subject is billed follows hour rules (ADR-20261103131742
// amendment 2): the plan in force at the hour prices it with that plan's
// policy from the policy's activation hour; Free hours, hours before the
// activation and hours without a policy bill nothing; usage above the
// allowance bills only while the subject's overage switch was on during the
// hour. Every hour's usage counts toward the month's allowance. The current
// provider delivers only the hours from the subject's billing_from on; a paid
// hour before it was billed by the provider it replaced (spec §10), so it
// still spends the cap, as compute's does. The pusher's spend cap reserve,
// meterd's cap hold and the ledger dual run all read this one
// implementation, so a byte the subject is not billed for never spends its
// cap (ADR-1162).

// egressBilledHour is one hour of a subject's egress as it is billed.
type egressBilledHour struct {
	hour time.Time
	// billed is what the subject pays for the hour before any spend cap.
	billed int64
	live   bool
	// delivered is whether the current provider delivers the hour: it is
	// from the subject's billing_from on.
	delivered bool
	policy    billing.MeterUsagePolicy
}

// billEgressHours applies the egress hour rules to a subject's hourly egress
// in one UTC month, rows in time order; rows from firstDelivered on are the
// current provider's. An hour of a plan with no egress policy is left out.
func billEgressHours(ctx context.Context, store state.Store, policies billing.MeterUsagePolicyProvider, grants *monthGrants, acct state.Account, rows []state.HourQuantity, firstDelivered int) ([]egressBilledHour, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	// prefix[i] is the usage of rows[:i].
	prefix := make([]int64, len(rows)+1)
	for i, row := range rows {
		var err error
		if prefix[i+1], err = addUsageQuantity(prefix[i], row.Quantity); err != nil {
			return nil, err
		}
	}
	first := rows[0].Hour
	monthStart := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	var choices []state.EgressOverageChoice
	if reader, ok := store.(egressOverageChoiceReader); ok {
		var err error
		if choices, err = reader.EgressOverageChoices(ctx, acct.ID, monthStart, rows[len(rows)-1].Hour.Add(time.Hour)); err != nil {
			return nil, fmt.Errorf("meter: read egress overage choices for %s: %w", acct.ID, err)
		}
	}
	var out []egressBilledHour
	for i := range rows {
		hour := rows[i].Hour
		plan, err := grants.planAt(ctx, acct, hour)
		if err != nil {
			return nil, err
		}
		policy, configured := policies.MeterUsagePolicy(plan, state.BillingMeterEgress)
		if plan == api.PlanFree || !configured {
			continue
		}
		if err := validateMeterUsagePolicy(policy); err != nil {
			return nil, err
		}
		if hour.Before(policy.EffectiveFrom) {
			continue
		}
		usageStart := monthStart
		if policy.EffectiveFrom.After(usageStart) {
			usageStart = policy.EffectiveFrom
		}
		from := sort.Search(len(rows), func(j int) bool { return !rows[j].Hour.Before(usageStart) })
		prior := prefix[i] - prefix[min(from, i)]
		billed := max(prior+rows[i].Quantity-policy.IncludedQuantity, 0) - max(prior-policy.IncludedQuantity, 0)
		if billed > 0 && !state.EgressOverageOnDuring(choices, hour, hour.Add(time.Hour)) {
			billed = 0
		}
		out = append(out, egressBilledHour{hour: hour, billed: billed, live: policy.Mode == billing.MeterDeliveryLive, delivered: i >= firstDelivered, policy: policy})
	}
	return out, nil
}

// errNoHourlyUsage reports a store that cannot read usage hour by hour.
var errNoHourlyUsage = errors.New("meter: store reads no hourly usage")

// monthLiveEgressBilled is the egress acct is billed in live delivery for
// the UTC month containing month, before through, by the hour rules, read
// through usage. It is what the spend cap reserves for egress. A provider
// that bills no egress returns 0; a store without hourly usage
// errNoHourlyUsage.
func monthLiveEgressBilled(ctx context.Context, usage usageReader, provider billing.Provider, acct state.Account, month, through time.Time) (int64, error) {
	policies, ok := provider.(billing.MeterUsagePolicyProvider)
	if !ok {
		return 0, nil
	}
	month, through = month.UTC(), through.UTC()
	monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	if monthEnd := monthStart.AddDate(0, 1, 0); through.After(monthEnd) {
		through = monthEnd
	}
	if !through.After(monthStart) {
		return 0, nil
	}
	rows, err := usage.hourly(ctx, acct.ID, state.BillingMeterEgress, monthStart, through)
	if err != nil {
		return 0, err
	}
	hours, err := billEgressHours(ctx, usage.store, policies, newMonthGrants(usage), acct, rows, 0)
	if err != nil {
		return 0, err
	}
	var billed int64
	for _, h := range hours {
		if h.live {
			if billed, err = addUsageQuantity(billed, h.billed); err != nil {
				return 0, err
			}
		}
	}
	return billed, nil
}
