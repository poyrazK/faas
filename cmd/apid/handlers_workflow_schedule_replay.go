package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewWorkflowScheduleReplays(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.workflowScheduleReplays(w, r, account, false)
}

func (s *server) replayWorkflowScheduleOccurrences(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.workflowScheduleReplays(w, r, account, true)
}

func (s *server) workflowScheduleReplays(w http.ResponseWriter, r *http.Request, account state.Account, execute bool) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.WorkflowScheduleReplayRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not decode workflow schedule replay selection"))
		return
	}
	if err := validateWorkflowScheduleReplayRequest(request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.WorkflowScheduleReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow schedule replay unavailable"))
		return
	}
	var results []state.WorkflowScheduleReplayResult
	var err error
	if execute {
		results, err = store.ReplayWorkflowScheduleOccurrences(r.Context(), app.ID, request.OccurrenceIDs)
	} else {
		results, err = store.PreviewWorkflowScheduleReplays(r.Context(), app.ID, request.OccurrenceIDs)
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("invalid workflow schedule replay selection"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to process workflow schedule replay selection"))
		return
	}
	response := api.WorkflowScheduleReplayResponse{Outcomes: make([]api.WorkflowScheduleReplayOutcome, 0, len(results))}
	for _, result := range results {
		response.Outcomes = append(response.Outcomes, api.WorkflowScheduleReplayOutcome{
			OccurrenceID: result.OccurrenceID, PlatformTenantID: result.PlatformTenantID, WorkflowName: result.WorkflowName,
			ScheduledFor: result.ScheduledFor, Outcome: result.Outcome, ReplayRunID: result.ReplayRunID,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func validateWorkflowScheduleReplayRequest(request api.WorkflowScheduleReplayRequest) error {
	if len(request.OccurrenceIDs) == 0 || len(request.OccurrenceIDs) > api.WorkflowScheduleReplayBatchMax {
		return errors.New("occurrence_ids must contain 1 to 20 UUIDs")
	}
	seen := make(map[string]struct{}, len(request.OccurrenceIDs))
	for i, raw := range request.OccurrenceIDs {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return errors.New("occurrence_ids must contain valid UUIDs")
		}
		request.OccurrenceIDs[i] = id.String()
		if _, exists := seen[id.String()]; exists {
			return errors.New("occurrence_ids must not contain duplicates")
		}
		seen[id.String()] = struct{}{}
	}
	return nil
}
