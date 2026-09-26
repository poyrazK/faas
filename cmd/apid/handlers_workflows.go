package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowRunResponse(r *state.WorkflowRun) api.WorkflowRunResponse {
	resp := api.WorkflowRunResponse{
		ID:           r.ID,
		AppID:        r.AppID,
		WorkflowName: r.WorkflowName,
		Status:       r.Status,
		CurrentStep:  r.CurrentStep,
		Input:        r.Input,
		Output:       r.Output,
		ScheduledFor: r.ScheduledFor.UTC().Format(time.RFC3339),
		LastError:    r.LastError,
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if r.StartedAt != nil {
		s := r.StartedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if r.FinishedAt != nil {
		s := r.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &s
	}
	return resp
}

func workflowStepResponse(s *state.WorkflowStep) api.WorkflowStepResponse {
	resp := api.WorkflowStepResponse{
		StepName:  s.StepName,
		Status:    s.Status,
		Attempt:   s.Attempt,
		Input:     s.Input,
		Output:    s.Output,
		Error:     s.Error,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
	}
	if s.StartedAt != nil {
		st := s.StartedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &st
	}
	if s.NextCheckAt != nil {
		next := s.NextCheckAt.UTC().Format(time.RFC3339)
		resp.NextCheckAt = &next
	}
	if s.FinishedAt != nil {
		ft := s.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &ft
	}
	return resp
}

func workflowStepAttemptResponse(a *state.WorkflowStepAttempt) api.WorkflowStepAttemptResponse {
	resp := api.WorkflowStepAttemptResponse{
		Attempt:    a.Attempt,
		Status:     a.Status,
		HTTPStatus: a.HTTPStatus,
		StartedAt:  a.StartedAt.UTC().Format(time.RFC3339),
		Error:      a.Error,
	}
	if a.FinishedAt != nil {
		finished := a.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &finished
	}
	if a.NextAttemptAt != nil {
		next := a.NextAttemptAt.UTC().Format(time.RFC3339)
		resp.NextAttemptAt = &next
	}
	return resp
}

// createWorkflowRun handles POST /v1/apps/{slug}/workflows/{name}/runs
func (s *server) createWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	workflowName := r.PathValue("name")
	if workflowName == "" {
		api.WriteProblem(w, api.ErrValidation("workflow name is required"))
		return
	}

	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}

	// Gating: check plan allows workflows
	if !acct.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(acct.Plan))
		return
	}
	if !s.workflowRuntimeEnabled {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}

	// Runs must snapshot a definition from the current live deployment.
	// This keeps a run deterministic even when a later deployment changes
	// the workflow, and avoids accepting a name that was never deployed.
	dep, err := s.store.LiveDeployment(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
		return
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(dep.Workflows, &definitions); err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployed workflow definitions are invalid"))
		return
	}
	var definition *api.WorkflowSpec
	for i := range definitions {
		if definitions[i].Name == workflowName {
			definition = &definitions[i]
			break
		}
	}
	if definition == nil {
		api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
		return
	}
	defSnapshot, err := json.Marshal(definition)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to snapshot workflow definition"))
		return
	}

	maxConcurrent := acct.Plan.WorkflowMaxConcurrentRuns()

	// Read input payload
	r.Body = http.MaxBytesReader(w, r.Body, api.WorkflowRunInputMaxBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowRunInputMaxBytes, api.WorkflowRunInputMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("failed to read request body"))
		return
	}

	inputRaw := json.RawMessage(`{}`)
	if len(bodyBytes) > 0 {
		if !json.Valid(bodyBytes) {
			api.WriteProblem(w, api.ErrValidation("request body must be valid JSON"))
			return
		}
		inputRaw = bodyBytes
	}

	run := &state.WorkflowRun{
		AppID:              app.ID,
		WorkflowName:       workflowName,
		Input:              inputRaw,
		DefinitionSnapshot: defSnapshot,
		Status:             state.WorkflowRunStatusPending,
		ScheduledFor:       time.Now().UTC(),
	}

	activeRuns, err := s.store.CreateWorkflowRunAdmitted(r.Context(), run, maxConcurrent)
	if errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
		api.WriteProblem(w, api.ErrPlanWorkflowsQuota(acct.Plan, maxConcurrent, activeRuns))
		return
	}
	if err != nil {
		s.log.Error("create workflow run failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to persist workflow run"))
		return
	}

	writeJSON(w, http.StatusCreated, workflowRunResponse(run))
}

// listWorkflowRuns handles GET /v1/apps/{slug}/workflows/runs
func (s *server) listWorkflowRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}

	opts := state.ListWorkflowRunsOpts{
		Limit:  50,
		Offset: 0,
		Status: r.URL.Query().Get("status"),
	}
	if !api.ValidWorkflowRunStatus(opts.Status) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid workflow status", "status must be pending, running, awaiting_event, succeeded, failed, or dead"))
		return
	}

	if limStr := r.URL.Query().Get("limit"); limStr != "" {
		if lim, err := strconv.Atoi(limStr); err == nil && lim > 0 {
			if lim > 100 {
				lim = 100
			}
			opts.Limit = lim
		}
	}

	if offStr := r.URL.Query().Get("offset"); offStr != "" {
		if off, err := strconv.Atoi(offStr); err == nil && off >= 0 {
			opts.Offset = off
		}
	}

	runs, total, err := s.store.ListWorkflowRuns(r.Context(), app.ID, opts)
	if err != nil {
		s.log.Error("list workflow runs failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to list workflow runs"))
		return
	}

	res := make([]api.WorkflowRunResponse, len(runs))
	for i, run := range runs {
		res[i] = workflowRunResponse(run)
	}

	writeJSON(w, http.StatusOK, api.ListWorkflowRunsResponse{
		Runs:  res,
		Total: total,
	})
}

// getWorkflowRun handles GET /v1/workflows/runs/{id}
func (s *server) getWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrWorkflowRunNotFound) {
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
			return
		}
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow run"))
		return
	}

	// Verify account owns the app
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}

// listWorkflowSteps handles GET /v1/workflows/runs/{id}/steps
func (s *server) listWorkflowSteps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	steps, err := s.store.GetWorkflowSteps(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow steps"))
		return
	}

	res := make([]api.WorkflowStepResponse, len(steps))
	for i, step := range steps {
		res[i] = workflowStepResponse(step)
	}

	writeJSON(w, http.StatusOK, api.ListWorkflowStepsResponse{
		Steps: res,
	})
}

// listWorkflowStepAttempts handles GET /v1/workflows/runs/{id}/steps/{step}/attempts.
func (s *server) listWorkflowStepAttempts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	attempts, err := s.store.GetWorkflowStepAttempts(r.Context(), id, r.PathValue("step"))
	if errors.Is(err, state.ErrWorkflowStepNotFound) {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow step attempts"))
		return
	}
	res := make([]api.WorkflowStepAttemptResponse, len(attempts))
	for i, attempt := range attempts {
		res[i] = workflowStepAttemptResponse(attempt)
	}
	writeJSON(w, http.StatusOK, api.ListWorkflowStepAttemptsResponse{Attempts: res})
}

// injectWorkflowEvent handles POST /v1/workflows/runs/{id}/events
func (s *server) injectWorkflowEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	var req api.InjectWorkflowEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}

	if req.EventName == "" {
		api.WriteProblem(w, api.ErrValidation("event_name is required"))
		return
	}
	if api.IsWorkflowCallbackEventName(req.EventName) {
		api.WriteProblem(w, api.ErrValidation("callback event names are reserved; use the callback completion endpoint"))
		return
	}

	// Events may arrive before schedd claims a newly-created run, and recording
	// an unrelated event atomically wakes an awaiting run back to pending for
	// re-evaluation. Accept every active state while continuing to reject
	// terminal runs.
	if run.Status != state.WorkflowRunStatusPending &&
		run.Status != state.WorkflowRunStatusRunning &&
		run.Status != state.WorkflowRunStatusAwaitingEvent {
		api.WriteProblem(w, api.ErrWorkflowNotRunning())
		return
	}

	evt := &state.WorkflowEvent{
		RunID:     run.ID,
		EventName: req.EventName,
		Payload:   req.Payload,
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		evt.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(run.ID+"\x00"+key)).String()
	}
	if err := s.store.InsertWorkflowEvent(r.Context(), evt); err != nil {
		s.log.Error("record workflow event failed", "run_id", run.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record workflow event"))
		return
	}

	writeJSON(w, http.StatusOK, api.InjectWorkflowEventResponse{
		Status:    "received",
		EventName: req.EventName,
	})
}

func (s *server) workflowRunForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) (*state.WorkflowRun, bool) {
	run, err := s.store.GetWorkflowRun(r.Context(), r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	return run, true
}

func workflowCallbackSpecs(run *state.WorkflowRun) ([]api.WorkflowStepSpec, error) {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return nil, err
	}
	var callbacks []api.WorkflowStepSpec
	for _, step := range spec.Steps {
		if step.WaitForCallback {
			callbacks = append(callbacks, step)
		}
	}
	return callbacks, nil
}

// listWorkflowCallbacks returns stable, account-authorized callback handles.
func (s *server) listWorkflowCallbacks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.workflowRunForAccount(w, r, acct)
	if !ok {
		return
	}
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return
	}
	steps, err := s.store.GetWorkflowSteps(r.Context(), run.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow steps"))
		return
	}
	started := make(map[string]time.Time, len(steps))
	for _, step := range steps {
		if step.StartedAt != nil {
			started[step.StepName] = *step.StartedAt
		}
	}
	response := api.ListWorkflowCallbacksResponse{Callbacks: make([]api.WorkflowCallbackResponse, 0, len(callbacks))}
	for _, step := range callbacks {
		entry := api.WorkflowCallbackResponse{ID: api.WorkflowCallbackID(run.ID, step.Name), StepName: step.Name}
		if activated, ok := started[step.Name]; ok {
			expires := activated.Add(step.Timeout).UTC().Format(time.RFC3339)
			entry.ExpiresAt = &expires
		}
		response.Callbacks = append(response.Callbacks, entry)
	}
	writeJSON(w, http.StatusOK, response)
}

// completeWorkflowCallback is authenticated like event injection. The ID is
// not a bearer token; no external caller gains authority from knowing it.
func (s *server) completeWorkflowCallback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.workflowRunForAccount(w, r, acct)
	if !ok {
		return
	}
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return
	}
	var selected *api.WorkflowStepSpec
	for i := range callbacks {
		if api.WorkflowCallbackID(run.ID, callbacks[i].Name) == r.PathValue("callback_id") {
			selected = &callbacks[i]
			break
		}
	}
	if selected == nil {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	var payload json.RawMessage
	if err := decodeJSON(r, &payload); err != nil {
		if !errors.Is(err, io.EOF) {
			api.WriteProblem(w, api.ErrValidation("callback body must be one JSON value within the request limit"))
			return
		}
	}
	duplicate, err := s.store.CompleteWorkflowCallback(r.Context(), run.ID, selected.Name,
		api.WorkflowCallbackEventName(run.ID, selected.Name), r.PathValue("callback_id"), selected.Timeout, payload)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrWorkflowCallbackExpired):
			api.WriteProblem(w, api.ErrWorkflowCallbackExpired())
		case errors.Is(err, state.ErrWorkflowCallbackClosed):
			api.WriteProblem(w, api.ErrWorkflowCallbackClosed())
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.ErrWorkflowCallbackPayloadConflict())
		default:
			s.log.Error("complete workflow callback failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to complete workflow callback"))
		}
		return
	}
	writeJSON(w, http.StatusOK, api.CompleteWorkflowCallbackResponse{Status: "received", Duplicate: duplicate})
}

// cancelWorkflowRun handles POST /v1/workflows/runs/{id}/cancel
func (s *server) cancelWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	if run.Status != state.WorkflowRunStatusSucceeded &&
		run.Status != state.WorkflowRunStatusFailed &&
		run.Status != state.WorkflowRunStatusDead {
		cancelErr := "cancelled by operator"
		run, err = s.store.CancelWorkflowRun(r.Context(), run.ID, cancelErr)
		if err != nil {
			s.log.Error("cancel workflow run failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to cancel workflow run"))
			return
		}
	}

	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}
