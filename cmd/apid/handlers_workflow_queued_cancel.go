package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewUnstartedWorkflowRunCancellations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowQueuedRunCancellations(w, r, acct, false)
}

func (s *server) cancelUnstartedWorkflowRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowQueuedRunCancellations(w, r, acct, true)
}

func (s *server) workflowQueuedRunCancellations(w http.ResponseWriter, r *http.Request, acct state.Account, execute bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.WorkflowQueuedRunCancelRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not decode workflow queued-run cancellation selection"))
		return
	}
	if err := validateWorkflowQueuedRunCancelRequest(&request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	response := api.WorkflowQueuedRunCancelResponse{Outcomes: make([]api.WorkflowQueuedRunCancelOutcome, 0, len(request.RunIDs))}
	if execute {
		store, ok := s.store.(state.WorkflowQueuedRunCancelStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("queued workflow cancellation is unavailable"))
			return
		}
		results, err := store.CancelUnstartedWorkflowRuns(r.Context(), app.ID, request.WorkflowName, request.RunIDs, "cancelled by operator")
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.ErrValidation("invalid workflow queued-run cancellation selection"))
			return
		}
		if err != nil {
			s.log.Error("cancel selected queued workflow runs failed", "app_id", logsanitize.Field(app.ID), "err", logsanitize.FieldAny(err))
			api.WriteProblem(w, api.ErrCapacity("failed to cancel selected queued workflow runs"))
			return
		}
		for _, result := range results {
			response.Outcomes = append(response.Outcomes, workflowQueuedRunCancelOutcome(result))
		}
	} else {
		for _, id := range request.RunIDs {
			run, err := s.store.GetWorkflowRun(r.Context(), id)
			if errors.Is(err, state.ErrWorkflowRunNotFound) || err == nil && run.AppID != app.ID {
				response.Outcomes = append(response.Outcomes, api.WorkflowQueuedRunCancelOutcome{RunID: id, Outcome: state.WorkflowQueuedRunCancelNotFound})
				continue
			}
			if err != nil {
				s.log.Error("preview selected queued workflow run failed", "app_id", logsanitize.Field(app.ID), "run_id", logsanitize.Field(id), "err", logsanitize.FieldAny(err))
				api.WriteProblem(w, api.ErrCapacity("failed to preview selected workflow runs"))
				return
			}
			outcome := state.ClassifyWorkflowRunForQueuedCancel(run, request.WorkflowName)
			response.Outcomes = append(response.Outcomes, workflowQueuedRunCancelOutcome(state.WorkflowQueuedRunCancelResult{
				RunID: id, WorkflowName: run.WorkflowName, Outcome: outcome, Status: run.Status,
				StartedAt: run.StartedAt, ScheduledFor: &run.ScheduledFor, CreatedAt: &run.CreatedAt, CancelledAt: run.CancelledAt,
			}))
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func validateWorkflowQueuedRunCancelRequest(request *api.WorkflowQueuedRunCancelRequest) error {
	if len(request.RunIDs) == 0 || len(request.RunIDs) > api.WorkflowQueuedRunCancelBatchMax {
		return fmt.Errorf("run_ids must contain 1 to %d UUIDs", api.WorkflowQueuedRunCancelBatchMax)
	}
	if request.WorkflowName != "" && len(request.WorkflowName) > api.WorkflowWebhookNameMaxBytes {
		return fmt.Errorf("workflow_name must contain at most %d bytes", api.WorkflowWebhookNameMaxBytes)
	}
	seen := make(map[string]struct{}, len(request.RunIDs))
	for i, rawID := range request.RunIDs {
		id, err := uuid.Parse(rawID)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("run_ids must contain valid UUIDs")
		}
		request.RunIDs[i] = id.String()
		if _, exists := seen[request.RunIDs[i]]; exists {
			return fmt.Errorf("run_ids must not contain duplicates")
		}
		seen[request.RunIDs[i]] = struct{}{}
	}
	return nil
}

func workflowQueuedRunCancelOutcome(result state.WorkflowQueuedRunCancelResult) api.WorkflowQueuedRunCancelOutcome {
	outcome := api.WorkflowQueuedRunCancelOutcome{
		RunID: result.RunID, WorkflowName: result.WorkflowName, Outcome: result.Outcome, Status: result.Status,
	}
	if result.StartedAt != nil {
		value := result.StartedAt.UTC().Format(time.RFC3339Nano)
		outcome.StartedAt = &value
	}
	if result.ScheduledFor != nil {
		value := result.ScheduledFor.UTC().Format(time.RFC3339Nano)
		outcome.ScheduledFor = &value
	}
	if result.CreatedAt != nil {
		value := result.CreatedAt.UTC().Format(time.RFC3339Nano)
		outcome.CreatedAt = &value
	}
	if result.CancelledAt != nil {
		value := result.CancelledAt.UTC().Format(time.RFC3339Nano)
		outcome.CancelledAt = &value
	}
	return outcome
}
