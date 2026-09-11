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
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "app no longer accepts deployments")
		return
	}
	api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
}
