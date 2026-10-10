package main

// ADR-732 fork exec routes. A command runs inside a running fork, the
// debug copy of production memory, so creating one and reading its output
// need the same scopes as creating the fork: deploy:write and secrets:read,
// with MFA. apid only records the command; the schedd holding the fork
// runs it through vmmd.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createAppForkExec(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, forkID, ok := s.loadAppForkPath(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAppForkExecRequest
	if err := decodeJSONSized(r, &req, api.AppForkExecRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid fork exec request body"))
		return
	}
	timeoutSeconds, maxOutput, problem := req.Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	exec, err := s.store.CreateAppForkExec(r.Context(), state.CreateAppForkExecParams{
		AccountID: acct.ID, AppID: app.ID, ForkID: forkID, RequestedBy: appForkActor(r, acct),
		Command: req.Command, CommandShell: req.Shell, TimeoutSeconds: timeoutSeconds, MaxOutputBytes: maxOutput,
		MaxPending: api.AppForkExecMaxPending, CreatedAt: time.Now().UTC(),
	})
	switch {
	case errors.Is(err, state.ErrAppForkExecRefused):
		api.WriteProblem(w, api.ErrAppForkExecRefused())
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not record the fork command"))
		return
	}
	// The command line can name secrets or customer data; audit only its
	// shape, never its arguments.
	s.audit.Emit(r.Context(), "app.fork_exec_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "fork_id": forkID, "exec_id": exec.ID, "requested_by": exec.RequestedBy,
		"argc": len(exec.Command), "shell": exec.CommandShell,
	})
	writeJSON(w, http.StatusAccepted, appForkExecResponse(exec))
}

func (s *server) getAppForkExec(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, forkID, ok := s.loadAppForkPath(w, r, acct)
	if !ok {
		return
	}
	execID := r.PathValue("exec_id")
	if _, err := uuid.Parse(execID); err != nil {
		s.notFound(w, "no such fork command")
		return
	}
	exec, err := s.store.AppForkExecByID(r.Context(), acct.ID, app.ID, forkID, execID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such fork command")
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load the fork command"))
		return
	}
	writeJSON(w, http.StatusOK, appForkExecResponse(exec))
}

func (s *server) listAppForkExecs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, forkID, ok := s.loadAppForkPath(w, r, acct)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			api.WriteProblem(w, api.ErrValidation("limit must be between 1 and 100"))
			return
		}
		limit = parsed
	}
	execs, err := s.store.ListAppForkExecs(r.Context(), acct.ID, app.ID, forkID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list fork commands"))
		return
	}
	out := api.AppForkExecListResponse{Items: make([]api.AppForkExecResponse, 0, len(execs))}
	for _, exec := range execs {
		out.Items = append(out.Items, appForkExecResponse(exec))
	}
	writeJSON(w, http.StatusOK, out)
}

func appForkExecResponse(e state.AppForkExec) api.AppForkExecResponse {
	resp := api.AppForkExecResponse{
		ID: e.ID, ForkID: e.ForkID, Command: e.Command, Shell: e.CommandShell, TimeoutSeconds: e.TimeoutSeconds,
		MaxOutputBytes: e.MaxOutputBytes, Status: api.AppForkExecStatus(e.Status), ExitCode: e.ExitCode,
		OutputTruncated: e.OutputTruncated, Stdout: string(e.Stdout), Stderr: string(e.Stderr),
		RequestedBy: e.RequestedBy, CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339Nano),
		StartedAt: appTaskTimeResponse(e.StartedAt), FinishedAt: appTaskTimeResponse(e.FinishedAt),
	}
	if e.FailureCode != nil && e.FailureMessage != nil {
		resp.Failure = &api.AppForkFailure{Code: *e.FailureCode, Message: *e.FailureMessage}
	}
	return resp
}
