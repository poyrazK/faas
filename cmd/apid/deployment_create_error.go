package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeDeploymentCreateError keeps lifecycle failures distinct from actual
// capacity failures. CreateDeployment uses ErrNotFound when the app vanished
// or became terminal after the handler's initial lookup.
func (s *server) writeDeploymentCreateError(w http.ResponseWriter, err error) {
	if p := routeRemovalBlockedProblem(err); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if state.IsBindingReleaseRequired(err) {
		api.WriteProblem(w, bindingReleaseRequiredProblem())
		return
	}
	if problem := api.AsProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "app no longer accepts deployments")
		return
	}
	if problem := state.ServiceCapacityProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
}
