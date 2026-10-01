// adr: 375
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func domainPublicationWriteProblem(err error) *api.Problem {
	var aggregate *state.TrafficPolicyAggregateError
	if errors.As(err, &aggregate) {
		problem := api.ErrTrafficPolicyAggregateTooLarge("domain_publication", aggregate.Unit, aggregate.Limit, aggregate.Limit+1)
		problem.Detail = "The domain binding could not be published within platform traffic policy limits; the claim remains unchanged"
		return problem.WithHint("Repair the affected policy, then retry. An operator can help when that policy belongs to another account.")
	}
	var analysis *state.TrafficPolicyAnalysisError
	if errors.As(err, &analysis) {
		problem := api.ErrTrafficPolicyTooComplex("domain_publication", analysis.Unit, analysis.Limit, analysis.Limit+1)
		problem.Detail = "Domain publication could not be verified within platform analysis limits; the claim remains unchanged"
		return problem
	}
	return api.ErrCapacity("could not create domain")
}
