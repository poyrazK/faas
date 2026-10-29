// adr: 570

// Package trafficproblem maps traffic-policy write refusals from pkg/state to
// RFC 7807 problems. It is shared by apidserver and the lifted apid domain
// packages (ADR-982) so every writer reports the same codes and bounds.
package trafficproblem

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Policy maps a refusal from an owner's own policy write; the exact witness
// scope and observed value belong to the caller. Other errors return fallback.
func Policy(err error, fallback *api.Problem) *api.Problem {
	var binding *state.TrafficPolicyBindingError
	if errors.As(err, &binding) {
		return AppBinding(err, fallback)
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

// TenantBinding maps a binding transition that can expose another account's
// policy. It returns a proven lower bound without its witness hostname, scope,
// or exact aggregate count.
func TenantBinding(err error, fallback *api.Problem) *api.Problem {
	return bindingProblem(err, fallback, "tenant_binding", "tenant binding")
}

// AppBinding is TenantBinding for app binding transitions.
func AppBinding(err error, fallback *api.Problem) *api.Problem {
	return bindingProblem(err, fallback, "app_binding", "app binding")
}

func bindingProblem(err error, fallback *api.Problem, scope, noun string) *api.Problem {
	var projection *state.TrafficPolicyProjectionError
	if errors.As(err, &projection) {
		return api.ErrTrafficPolicyTooLarge(scope, projection.Limit, projection.Limit+1)
	}
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		problem := api.ErrTrafficPolicyAggregateTooLarge(scope, aggregate.Unit, aggregate.Limit, aggregate.Limit+1)
		problem.Detail = "The " + noun + " change would expose a traffic policy above platform limits; intent remains unchanged"
		return problem.WithHint("Repair the affected policy, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex(scope, analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "The " + noun + " change could not be verified within platform analysis limits; intent remains unchanged"
		return problem
	}
	return fallback
}

// DomainRemoval maps a refused custom-domain detachment. The exposed policy
// can belong to another account, so only a proven lower bound is reported.
func DomainRemoval(err error) *api.Problem {
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		minimum := aggregate.Limit + 1
		problem := api.ErrTrafficPolicyAggregateTooLarge("domain_removal", aggregate.Unit, aggregate.Limit, minimum)
		problem.Detail = fmt.Sprintf("Removing this domain would expose a traffic policy with at least %d %s, above the %d cap; the domain remains attached", minimum, aggregate.Unit, aggregate.Limit)
		return problem.WithHint("Repair the policy the removal would expose, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex("domain_removal", analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "Domain removal could not be verified within platform analysis limits; the domain remains attached"
		return problem
	}
	return api.ErrCapacity("could not delete domain")
}
