package main

// Public deployment-attached app-task admission and lifecycle handlers
// (ADR-230). apid selects the current live deployment once; the state layer
// atomically copies its runtime artifact identity into the durable task row.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func appTaskAPIDisabledProblem() *api.Problem {
	return api.NewProblem(
		http.StatusNotImplemented,
		api.CodeNotImplemented,
		"App task API unavailable",
		"deployment-attached app tasks are not enabled on this control-plane host",
	)
}

func (s *server) requireAppTaskAPI(w http.ResponseWriter) bool {
	if s.appTaskAPIEnabled {
		return true
	}
	api.WriteProblem(w, appTaskAPIDisabledProblem())
	return false
}

func appTaskResponse(row state.AppTask) api.AppTaskResponse {
	resp := api.AppTaskResponse{
		WorkDecision:        row.WorkDecision,
		OutcomeCode:         row.OutcomeCode,
		ID:                  row.ID,
		AppID:               row.AppID,
		DeploymentID:        row.DeploymentID,
		DeploymentScope:     row.DeploymentScope,
		Kind:                api.AppTaskKind(row.Kind),
		Command:             append([]string(nil), row.Command...),
		CommandShell:        row.CommandShell,
		Status:              api.AppTaskStatus(row.Status),
		TimeoutSeconds:      row.TimeoutSeconds,
		MaxOutputBytes:      row.MaxOutputBytes,
		RetryMax:            row.RetryMax,
		RetryBackoffSeconds: row.RetryBackoffSeconds,
		AttemptCount:        row.AttemptCount,
		StdoutTail:          row.StdoutTail,
		StderrTail:          row.StderrTail,
		OutputTruncated:     row.OutputTruncated,
		CreatedAt:           row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:           row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if row.ExitCode != nil {
		value := *row.ExitCode
		resp.ExitCode = &value
	}
	if row.FailureCode != nil && row.FailureMessage != nil {
		resp.Failure = &api.AppTaskFailure{Code: *row.FailureCode, Message: *row.FailureMessage}
	}
	resp.CancelRequestedAt = appTaskTimeResponse(row.CancelRequested)
	resp.StartedAt = appTaskTimeResponse(row.StartedAt)
	resp.FinishedAt = appTaskTimeResponse(row.FinishedAt)
	resp.RetryAt = appTaskTimeResponse(row.RetryAt)
	return resp
}

func appTaskTimeResponse(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func (s *server) createAppTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	var request api.CreateAppTaskRequest
	if err := decodeJSONSized(r, &request, api.AppTaskRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AppTaskRequestMaxBytes, api.AppTaskRequestMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("invalid app task request body"))
		return
	}
	resolved, problem := request.Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	row, problem := s.admitAppTask(r, acct, app, resolved)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusAccepted, appTaskResponse(row))
}

func (s *server) admitAppTask(r *http.Request, acct state.Account, app state.App, resolved api.ResolvedCreateAppTaskRequest) (state.AppTask, *api.Problem) {
	if api.IsServiceBindingSmokeCommand(resolved.Command, resolved.CommandShell) && !declaresSmokeService(app, resolved.Command[1]) {
		return state.AppTask{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Service binding unavailable", "The selected service is not declared for this app.")
	}
	deployment, problem := s.selectAppTaskDeployment(r.Context(), app, resolved)
	if problem != nil {
		return state.AppTask{}, problem
	}
	pin, pinErr := s.captureBindingVerificationPin(r, acct, app, deployment, resolved)
	if pinErr != nil {
		return state.AppTask{}, api.ErrInternal("could not capture binding verification metadata")
	}
	if (resolved.VerificationDeploymentID != "" || len(resolved.Command) > 0 && resolved.Command[0] == api.AppTaskOutboundBindingProbeCommand) && pin == nil {
		return state.AppTask{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Binding verification unavailable", "The selected binding is not available for verification with this request.")
	}
	command, err := bindingVerificationTaskCommand(resolved.Command, pin)
	if err != nil {
		return state.AppTask{}, api.ErrInternal("could not encode outbound probe")
	}
	row, err := s.store.CreateAppTask(r.Context(), state.CreateAppTaskParams{
		RequireLiveDeployment: resolved.VerificationDeploymentID != "" || resolved.SmokeDeploymentID != "",
		BindingVerification:   pin,
		AccountID:             acct.ID,
		AppID:                 app.ID,
		DeploymentID:          deployment.ID,
		Kind:                  state.AppTaskKindManual,
		Command:               command,
		CommandShell:          resolved.CommandShell,
		TimeoutSeconds:        resolved.TimeoutSeconds,
		MaxOutputBytes:        resolved.MaxOutputBytes,
		CreatedAt:             time.Now().UTC(),
	})
	if errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		return state.AppTask{}, appTaskDeploymentUnavailableProblem()
	}
	if errors.Is(err, state.ErrAppTaskInvalid) {
		return state.AppTask{}, api.ErrValidation("invalid app task request")
	}
	if err != nil {
		return state.AppTask{}, api.ErrInternal("could not persist app task admission")
	}
	return row, nil
}

func declaresSmokeService(app state.App, service string) bool {
	for _, item := range serviceBindingInventory(app) {
		if item.Name == service {
			return true
		}
	}
	return false
}

func writeAppTaskDeploymentUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, appTaskDeploymentUnavailableProblem())
}

func appTaskDeploymentUnavailableProblem() *api.Problem {
	return api.NewProblem(
		http.StatusConflict,
		api.CodeConflict,
		"App task deployment unavailable",
		"the app needs a live deployment with a materialized runtime image before it can run a task",
	)
}

func (s *server) listAppTasks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limitProblem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 50, 200, "app tasks")
	if limitProblem != nil {
		api.WriteProblem(w, limitProblem)
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad offset", "offset must be a non-negative integer"))
			return
		}
		offset = parsed
	}

	rows, err := s.store.ListAppTasks(r.Context(), acct.ID, app.ID, limit, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list app tasks"))
		return
	}
	tasks := make([]api.AppTaskResponse, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, appTaskResponse(row))
	}
	nextOffset := -1
	if len(tasks) == limit {
		probe, probeErr := s.store.ListAppTasks(r.Context(), acct.ID, app.ID, 1, offset+limit)
		if probeErr != nil {
			api.WriteProblem(w, api.ErrInternal("could not list app tasks"))
			return
		}
		if len(probe) != 0 {
			nextOffset = offset + len(tasks)
		}
	}
	writeJSON(w, http.StatusOK, api.AppTaskListResponse{
		Tasks: tasks, Limit: limit, Offset: offset, NextOffset: nextOffset,
	})
}

func (s *server) getAppTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such app task")
		return
	}
	row, err := s.store.AppTaskByID(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app task")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load app task"))
		return
	}
	writeJSON(w, http.StatusOK, appTaskResponse(row))
}

func (s *server) cancelAppTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such app task")
		return
	}
	row, err := s.store.RequestAppTaskCancellation(
		r.Context(), acct.ID, app.ID, id, time.Now().UTC(),
	)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app task")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not cancel app task"))
		return
	}
	writeJSON(w, http.StatusAccepted, appTaskResponse(row))
}
