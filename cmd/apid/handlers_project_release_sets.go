package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) publishProjectReleaseSet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	c, ok := s.readProjectReleaseCandidate(w, r, acct)
	if !ok {
		return
	}
	var response api.ProjectReleaseSetResponse
	if c.request.ExpectedActiveReleaseID != nil {
		checked, problem := s.publishCheckedProjectRelease(r, acct, c)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		response = checked
	} else {
		store, ok := s.store.(state.ProjectReleaseSetStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("project release sets are unavailable"))
			return
		}
		release, err := store.PublishProjectReleaseSet(ctx, acct.ID, c.project.ID, c.environment, c.request.TTLSeconds, c.members)
		if problem := projectReleasePublishProblem(err); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		response = projectReleaseSetResponse(release)
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "project.release_set_published", &acct.ID, map[string]any{"project_id": c.project.ID, "environment": c.environment, "release_id": response.ID, "bindings_check": response.BindingsCheck})
	}
	writeJSON(w, http.StatusCreated, response)
}

func projectReleasePublishProblem(err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case state.IsBindingReleaseRequired(err):
		return api.NewProblem(http.StatusConflict, api.CodeBindingReleaseRequired, "Project release requires binding checks", "Set expected_active_release_id (empty for no active graph) to qualify every exact member and activate atomically.")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment not found", "No such project environment.")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrValidation("invalid release set deployment ID or TTL")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Release set cannot be published", "All project workloads need a live eligible deployment and sufficient revision retention.")
	default:
		return api.ErrCapacity("could not publish project release set")
	}
}
