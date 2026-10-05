package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func plainReplayEligibility(orig state.Invocation) *api.Problem {
	if orig.State != state.InvocationFailed && orig.State != state.InvocationDeadLetter {
		return api.ErrInvocationNotReplayable(string(orig.State))
	}
	return genericReplayPolicyProblem(orig)
}

func (s *server) plainReplayTarget(r *http.Request, acct state.Account) (state.Invocation, state.App, *api.Problem) {
	id := r.PathValue("id")
	orig, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || orig.AccountID != acct.ID {
		return state.Invocation{}, state.App{}, api.ErrInvocationNotFound(id)
	}
	app, err := s.store.AppByID(r.Context(), orig.AppID)
	if err != nil || app.AccountID != acct.ID {
		return state.Invocation{}, state.App{}, api.ErrInvocationNotFound(id)
	}
	if !app.AcceptsRequestInvocations() {
		return state.Invocation{}, state.App{}, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
	}
	return orig, app, plainReplayEligibility(orig)
}

func (s *server) servePlainReplay(w http.ResponseWriter, r *http.Request, acct state.Account, orig state.Invocation, retryPolicy json.RawMessage, statusPrefix string) {
	store, ok := s.store.(state.PlainInvocationReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("plain replay store unavailable"))
		return
	}
	inv, err := store.ExistingPlainInvocationReplay(r.Context(), acct.ID, orig.ID)
	if errors.Is(err, state.ErrNotFound) {
		opts, problem := s.invocationReplayOptions(r, acct, orig)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		inv, err = store.ReplayPlainInvocation(r.Context(), acct.ID, orig.ID, state.PlainInvocationReplayOptions{
			Headers: opts.Headers, RetryPolicyJSON: retryPolicy,
			DeadlineAt: opts.DeadlineAt, ResultRetentionUntil: opts.ResultRetentionUntil,
		})
	}
	if problem := plainReplayProblem(err, orig.ID); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	setInvocationVersionResponseHeaders(w, inv)
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{ID: inv.ID, StatusURL: statusPrefix + inv.ID})
}

func plainReplayProblem(err error, id string) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return api.ErrInvocationNotFound(id)
	case errors.Is(err, state.ErrPlainReplayNotAllowed):
		return api.NewProblem(http.StatusConflict, api.CodeInvocationNotReplayable, "Invocation not replayable", "only failed or dead-lettered unbound unkeyed work can use this recovery path")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "invocation_replay_unavailable", "Invocation replay unavailable", "the existing replay is no longer retained; this execution cannot create another replay")
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Platform tenant suspended", "resume this customer before replaying work")
	default:
		return api.ErrCapacity("enqueue replay invocation")
	}
}
