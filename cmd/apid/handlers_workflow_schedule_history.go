package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listWorkflowScheduleOccurrences(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	options, err := workflowScheduleHistoryOptions(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.WorkflowScheduleHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow schedule history unavailable"))
		return
	}
	rows, err := store.ListWorkflowScheduleOccurrences(r.Context(), app.ID, options.PlatformTenantID, options.Cursor, options.Limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow schedule history"))
		return
	}
	writeJSON(w, http.StatusOK, workflowScheduleHistoryResponse(rows, options.Limit))
}

func workflowScheduleHistoryOptions(r *http.Request) (api.ListWorkflowScheduleOccurrencesOptions, error) {
	options := api.ListWorkflowScheduleOccurrencesOptions{Limit: api.WorkflowScheduleHistoryPageDefault,
		Cursor: r.URL.Query().Get("cursor"), PlatformTenantID: r.URL.Query().Get("platform_tenant_id")}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > api.WorkflowScheduleHistoryPageMax {
			return options, errors.New("invalid occurrence history limit")
		}
		options.Limit = value
	}
	for _, value := range []*string{&options.Cursor, &options.PlatformTenantID} {
		if *value == "" {
			continue
		}
		parsed, err := uuid.Parse(*value)
		if err != nil {
			return options, errors.New("cursor and platform_tenant_id must be UUIDs")
		}
		*value = parsed.String()
	}
	return options, nil
}

func workflowScheduleHistoryResponse(rows []state.WorkflowScheduleOccurrence, limit int) api.ListWorkflowScheduleOccurrencesResponse {
	response := api.ListWorkflowScheduleOccurrencesResponse{Occurrences: make([]api.WorkflowScheduleOccurrenceResponse, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		rows = rows[:limit]
		response.NextCursor = rows[len(rows)-1].ID
	}
	for _, row := range rows {
		response.Occurrences = append(response.Occurrences, api.WorkflowScheduleOccurrenceResponse{
			ID: row.ID, AppID: row.AppID, PlatformTenantID: row.PlatformTenantID, WorkflowName: row.WorkflowName, DeploymentID: row.DeploymentID,
			ScheduledFor: row.ScheduledFor, EvaluatedAt: row.EvaluatedAt, Status: row.Status, RunID: row.RunID,
		})
	}
	return response
}
