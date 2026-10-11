package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getDevPatchStatus reports whether a published developer live patch reached
// the running environment (ADR-740). It is read-only and never renews the
// developer lease.
func (s *server) getDevPatchStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project := r.PathValue("project")
	generation, err := strconv.ParseInt(r.PathValue("generation"), 10, 64)
	if !validSlug(project) || err != nil || generation < 1 {
		s.notFound(w, "no such developer live patch")
		return
	}
	workspaceID := r.URL.Query().Get("workspace_id")
	if !validDevWorkspaceID(workspaceID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workspace ID", "workspace_id must be 32 lowercase hexadecimal characters"))
		return
	}
	app, ok := s.developerSessionApp(r, acct, project, workspaceID)
	store, storeOK := s.store.(state.DevSourcePatchStore)
	if !ok || !storeOK {
		s.notFound(w, "no such developer live patch")
		return
	}
	status, err := store.DevSourcePatchStatus(r.Context(), app.ID, generation)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such developer live patch")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("load developer live patch"))
		return
	}
	writeJSON(w, http.StatusOK, devPatchStatusResponse(status))
}

func devPatchStatusResponse(status state.DevSourcePatchStatus) api.DevPatchStatusResponse {
	response := api.DevPatchStatusResponse{Generation: status.Generation, State: api.DevPatchStatePending, CreatedAt: status.CreatedAt}
	if status.AppliedAt != nil {
		response.AppliedAt, response.ApplyMS = status.AppliedAt, status.ApplyMS
		response.State = api.DevPatchStateApplied
		if status.ApplyError != "" {
			response.State, response.ErrorCode = api.DevPatchStateFailed, status.ApplyError
		}
	}
	return response
}
