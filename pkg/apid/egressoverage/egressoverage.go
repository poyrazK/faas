// Package egressoverage holds apid's egress overage choice rules
// (ADR-20261103131742 amendment 2): a paid billing subject may let its apps
// keep serving past the plan's monthly egress, billed per GiB under its spend
// cap. Off is the default, and Free cannot turn it on.
package egressoverage

import "github.com/onebox-faas/faas/pkg/api"

// Response is the API view of a subject's choice on plan. billedLive reports
// whether Gregale bills egress live for plan.
func Response(plan api.Plan, enabled, billedLive bool) api.EgressOverageResponse {
	var included int64
	if limits, ok := api.LimitsFor(plan); ok {
		included = limits.MonthlyEgressGiB
	}
	return api.EgressOverageResponse{
		Enabled:          enabled,
		Billed:           billedLive && plan != api.PlanFree,
		IncludedGiB:      included,
		MillicentsPerGiB: api.EgressOverageMillicentsPerGiB,
	}
}

// Refusal is the problem for choosing enabled on plan, or nil. Free egress is
// never billed, so a Free subject cannot turn overage on; turning it off is
// always allowed.
func Refusal(plan api.Plan, enabled bool) *api.Problem {
	if enabled && plan == api.PlanFree {
		return api.ErrPlanEgressOverageNotAllowed(plan)
	}
	return nil
}
