package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func genericReplayPolicyProblem(orig state.Invocation) *api.Problem {
	if orig.QueueBindingID != "" || orig.QueueName != "" {
		return api.NewProblem(http.StatusConflict, "queue_replay_requires_binding", "Queue replay requires its binding",
			"use the app queue dead-letter replay endpoint to retain the original binding and work policy")
	}
	if orig.WorkPolicyName != "" {
		return api.NewProblem(http.StatusConflict, "keyed_replay_requires_policy", "Keyed replay requires its policy",
			"use POST /v1/invocations/{id}/replay-keyed for failed work or the app queue dead-letter replay endpoint for dead-lettered work")
	}
	return nil
}

func (s *server) replayKeyedInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orig, problem := s.keyedReplayTarget(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.KeyedInvocationReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("keyed replay store unavailable"))
		return
	}
	inv, err := store.ExistingKeyedInvocationReplay(r.Context(), acct.ID, orig.ID)
	if !errors.Is(err, state.ErrNotFound) {
		writeKeyedReplay(w, inv, err, orig.ID)
		return
	}
	opts, problem := s.invocationReplayOptions(r, acct, orig)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	inv, err = store.ReplayKeyedInvocation(r.Context(), acct.ID, orig.ID, opts)
	writeKeyedReplay(w, inv, err, orig.ID)
}

func writeKeyedReplay(w http.ResponseWriter, inv state.Invocation, err error, parentID string) {
	if problem := keyedReplayProblem(err, parentID); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	setInvocationVersionResponseHeaders(w, inv)
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{ID: inv.ID, StatusURL: "/v1/invocations/" + inv.ID})
}

func (s *server) invocationReplayOptions(r *http.Request, acct state.Account, orig state.Invocation) (state.KeyedInvocationReplayOptions, *api.Problem) {
	headers, err := pkgtrace.MergeHeaders(r.Context(), orig.Headers)
	if err != nil {
		return state.KeyedInvocationReplayOptions{}, api.ErrValidation("original invocation headers must be a JSON object of string values")
	}
	orig.Headers = headers
	prepared, problem := s.prepareInvocationVersion(r.Context(), nil, orig)
	if problem != nil {
		return state.KeyedInvocationReplayOptions{}, problem
	}
	return state.KeyedInvocationReplayOptions{
		Headers: prepared.Headers, DeadlineAt: deadlineForRequest(nil, acct), ResultRetentionUntil: retentionForRequest(nil, acct),
	}, nil
}

func (s *server) keyedReplayTarget(r *http.Request, acct state.Account) (state.Invocation, *api.Problem) {
	id := r.PathValue("id")
	orig, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || orig.AccountID != acct.ID {
		return state.Invocation{}, api.ErrInvocationNotFound(id)
	}
	app, err := s.store.AppByID(r.Context(), orig.AppID)
	if err != nil || app.AccountID != acct.ID {
		return state.Invocation{}, api.ErrInvocationNotFound(id)
	}
	if !app.AcceptsRequestInvocations() {
		return state.Invocation{}, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
	}
	if orig.State != state.InvocationFailed || orig.WorkPolicyName == "" || orig.QueueBindingID != "" || orig.QueueName != "" {
		return state.Invocation{}, keyedReplayProblem(state.ErrKeyedReplayNotAllowed, id)
	}
	return orig, nil
}

func keyedReplayProblem(err error, id string) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrEventDeliveryCapacity):
		return eventDeliveryReplayProblem(err, "")
	case errors.Is(err, state.ErrNotFound):
		return api.ErrInvocationNotFound(id)
	case errors.Is(err, state.ErrKeyedReplayNotAllowed):
		return api.NewProblem(http.StatusConflict, "keyed_replay_not_allowed", "Keyed replay unavailable", "only failed keyed work without a queue binding can use this recovery path")
	case errors.Is(err, state.ErrKeyedReplayExpired):
		return api.NewProblem(http.StatusConflict, "keyed_replay_expired", "Keyed replay deadline expired", "the original pending deadline has elapsed; publish new work to request a fresh lifetime")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "keyed_replay_unavailable", "Keyed replay unavailable", "the existing replay is no longer retained; this execution cannot create another replay")
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Platform tenant suspended", "resume this customer before replaying work")
	default:
		return api.ErrCapacity("enqueue keyed replay")
	}
}
