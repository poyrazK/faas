package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func tenantWorkflowScheduleResponse(schedule state.TenantWorkflowSchedule) api.TenantWorkflowScheduleResponse {
	return api.TenantWorkflowScheduleResponse{WorkflowName: schedule.WorkflowName, DeploymentID: schedule.DeploymentID,
		Schedule: schedule.Schedule, Timezone: schedule.Timezone, Overlap: schedule.Overlap, Enabled: schedule.Enabled,
		TenantConfigurable: true, Customized: schedule.Customized, Version: schedule.Version}
}

func (s *server) listPlatformTenantSelfWorkflowSchedules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, app, ok := s.platformTenantEventApp(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.TenantWorkflowScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("tenant workflow schedule store unavailable"))
		return
	}
	schedules, err := store.ListTenantWorkflowSchedules(r.Context(), acct.ID, tenantID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "app not found")
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "list tenant workflow schedules failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list tenant workflow schedules"))
		return
	}
	response := api.ListTenantWorkflowSchedulesResponse{Schedules: make([]api.TenantWorkflowScheduleResponse, 0, len(schedules))}
	for _, schedule := range schedules {
		response.Schedules = append(response.Schedules, tenantWorkflowScheduleResponse(schedule))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) updatePlatformTenantSelfWorkflowSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, app, ok := s.platformTenantEventApp(w, r, acct)
	if !ok {
		return
	}
	var request api.UpdateTenantWorkflowScheduleRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if request.ExpectedVersion == nil || *request.ExpectedVersion < 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version is required and must be zero or greater"))
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	store, ok := s.store.(state.TenantWorkflowScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("tenant workflow schedule store unavailable"))
		return
	}
	schedule, err := store.UpdateTenantWorkflowSchedule(r.Context(), acct.ID, tenantID, app.ID, r.PathValue("name"),
		*request.ExpectedVersion, request.Schedule, request.Timezone, request.Overlap, enabled)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "tenant-configurable workflow schedule not found")
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Workflow schedule version conflict", "reload the tenant workflow schedules and retry with the current version"))
		return
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid workflow schedule", "schedule, timezone, overlap, or enabled state is invalid for this workflow"))
		return
	case err != nil:
		s.log.ErrorContext(r.Context(), "update tenant workflow schedule failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not update tenant workflow schedule"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, tenantWorkflowScheduleResponse(schedule))
}
