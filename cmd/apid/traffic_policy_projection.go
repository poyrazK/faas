// adr: 375
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func trafficPolicyWriteProblem(err error, fallback *api.Problem) *api.Problem {
	var binding *state.TrafficPolicyBindingError
	if errors.As(err, &binding) {
		return appBindingWriteProblem(err, fallback)
	}
	var projection *state.TrafficPolicyProjectionError
	if errors.As(err, &projection) {
		return api.ErrTrafficPolicyTooLarge(projection.Scope, projection.Limit, projection.Observed)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		return api.ErrTrafficPolicyAggregateTooLarge(aggregate.Scope, aggregate.Unit, aggregate.Limit, aggregate.Observed)
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		return api.ErrTrafficPolicyTooComplex(analysis.Scope, analysis.Unit, analysis.Limit, analysis.Observed)
	}
	return fallback
}

// A binding transition can expose another account's policy. Return a proven
// lower bound without its witness hostname, scope, or exact aggregate count.
func tenantBindingWriteProblem(err error, fallback *api.Problem) *api.Problem {
	var projection *state.TrafficPolicyProjectionError
	if errors.As(err, &projection) {
		return api.ErrTrafficPolicyTooLarge("tenant_binding", projection.Limit, projection.Limit+1)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		problem := api.ErrTrafficPolicyAggregateTooLarge("tenant_binding", aggregate.Unit, aggregate.Limit, aggregate.Limit+1)
		problem.Detail = "The tenant binding change would expose a traffic policy above platform limits; intent remains unchanged"
		return problem.WithHint("Repair the affected policy, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex("tenant_binding", analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "The tenant binding change could not be verified within platform analysis limits; intent remains unchanged"
		return problem
	}
	return fallback
}

func appBindingWriteProblem(err error, fallback *api.Problem) *api.Problem {
	var projection *state.TrafficPolicyProjectionError
	if errors.As(err, &projection) {
		return api.ErrTrafficPolicyTooLarge("app_binding", projection.Limit, projection.Limit+1)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		problem := api.ErrTrafficPolicyAggregateTooLarge("app_binding", aggregate.Unit, aggregate.Limit, aggregate.Limit+1)
		problem.Detail = "The app binding change would expose a traffic policy above platform limits; intent remains unchanged"
		return problem.WithHint("Repair the affected policy, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex("app_binding", analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "The app binding change could not be verified within platform analysis limits; intent remains unchanged"
		return problem
	}
	return fallback
}
