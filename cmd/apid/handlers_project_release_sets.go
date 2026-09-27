package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) publishProjectReleaseSet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environment := r.PathValue("environment")
	if !api.ValidProjectEnvironmentSlug(environment) {
		api.WriteProblem(w, api.ErrValidation("invalid project environment"))
		return
	}
	var req api.PublishProjectReleaseSetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.TTLSeconds <= 0 || req.TTLSeconds > api.RevisionPinMaxTTLSeconds || len(req.Deployments) == 0 || len(req.Deployments) > api.ProjectReleaseSetMaxMembers {
		api.WriteProblem(w, api.ErrValidation("release set requires 1-100 workloads and ttl_seconds within the revision pin window"))
		return
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project workloads"))
		return
	}
	members := make([]state.ProjectReleaseMember, 0, len(apps))
	if len(apps) != len(req.Deployments) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Incomplete release set", "every project workload must have exactly one deployment"))
		return
	}
	for _, app := range apps {
		depID := req.Deployments[app.Slug]
		if depID == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Incomplete release set", "every project workload must have exactly one deployment"))
			return
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: depID})
	}
	store, ok := s.store.(state.ProjectReleaseSetStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project release sets are unavailable"))
		return
	}
	release, err := store.PublishProjectReleaseSet(r.Context(), acct.ID, project.ID, environment, req.TTLSeconds, members)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "project environment not found")
		return
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("invalid release set deployment ID or TTL"))
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Release set cannot be published", "all project workloads need a live eligible deployment (traffic-bearing, explicitly dark, or retained) and revision_pin_ttl_seconds at least as long as the release TTL"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not publish project release set"))
		return
	}
	s.audit.Emit(r.Context(), "project.release_set_published", &acct.ID, map[string]any{"project_id": project.ID, "environment": environment, "release_id": release.ID})
	writeJSON(w, http.StatusCreated, release)
}
