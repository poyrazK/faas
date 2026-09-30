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
	return fallback
}
