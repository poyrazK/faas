package billing

// adr: 20261104204500

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EgressOverageElectionFor reads subjectID's egress overage election from
// store. A store that keeps no elections reports none, so egress above the
// allowance is never billed through it (ADR-20261104204500).
func EgressOverageElectionFor(ctx context.Context, store any, subjectID string) (state.EgressOverageElection, error) {
	elections, ok := store.(state.EgressOverageReader)
	if !ok {
		return state.EgressOverageElection{}, nil
	}
	return elections.EgressOverageElection(ctx, subjectID)
}

// EgressOverageLifted reports whether plan's egress ceiling is lifted: the
// plan allows the election, the subject's election is on and the provider
// bills the plan's egress live as of now. Otherwise egress past the
// allowance stops the subject (ADR-20261104090000, ADR-20261104204500).
func EgressOverageLifted(provider Provider, plan api.Plan, election state.EgressOverageElection, now time.Time) bool {
	limits, ok := api.LimitsFor(plan)
	if !ok || !limits.EgressOverageElectable || !election.On() {
		return false
	}
	policies, ok := provider.(MeterUsagePolicyProvider)
	if !ok {
		return false
	}
	policy, configured := policies.MeterUsagePolicy(plan, state.BillingMeterEgress)
	return configured && policy.Mode == MeterDeliveryLive && !policy.EffectiveFrom.After(now)
}

// ElectedEgressOverage is the part of a period's egress above included that
// was sent in hours the election covers. Hours are taken in order; an hour's
// overage is how far it lifted the running total above included, and it
// counts only when the election covers that hour, as meterd bills it
// (ADR-20261104204500). rows are UsageByHour rows (Month holds the hour).
func ElectedEgressOverage(rows []state.Usage, included int64, election state.EgressOverageElection) int64 {
	if election.EnabledAt == nil {
		return 0
	}
	included = max(included, 0)
	byHour := make(map[time.Time]int64)
	for _, row := range rows {
		if row.NetTxBytes <= 0 {
			continue
		}
		hour := row.Month.UTC().Truncate(time.Hour)
		byHour[hour] = addSaturating(byHour[hour], row.NetTxBytes)
	}
	hours := make([]time.Time, 0, len(byHour))
	for hour := range byHour {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	var total, billed int64
	for _, hour := range hours {
		before := max(total-included, 0)
		total = addSaturating(total, byHour[hour])
		if election.Covers(hour) {
			billed = addSaturating(billed, max(total-included, 0)-before)
		}
	}
	return billed
}

func addSaturating(left, right int64) int64 {
	if right > math.MaxInt64-left {
		return math.MaxInt64
	}
	return left + right
}
