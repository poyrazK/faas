// adr: 375
package main

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func trafficPolicyWriteProblem(err error, fallback *api.Problem) *api.Problem {
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
