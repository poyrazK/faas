package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantSelfInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Invocation, bool) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return state.Invocation{}, false
	}
	if _, ok := s.platformTenantStore(w, acct); !ok {
		return state.Invocation{}, false
	}
	inv, err := s.store.InvocationByID(r.Context(), r.PathValue("id"))
	if err != nil || inv.AccountID != acct.ID || inv.PlatformTenantID != tenantID {
		api.WriteProblem(w, api.ErrInvocationNotFound(r.PathValue("id")))
		return state.Invocation{}, false
	}
	app, err := s.store.AppByID(r.Context(), inv.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrInvocationNotFound(r.PathValue("id")))
		return state.Invocation{}, false
	}
	return inv, true
}

func platformTenantInvocationResponse(inv state.Invocation) api.PlatformTenantInvocationResponse {
	var outcome *string
	if inv.Outcome != nil {
		value := string(*inv.Outcome)
		outcome = &value
	}
	return api.PlatformTenantInvocationResponse{ID: inv.ID, State: string(inv.State),
		Method: inv.Method, Path: inv.Path, Attempts: inv.Attempts, Result: inv.Result,
		LastError: inv.LastError, Outcome: outcome, CreatedAt: inv.CreatedAt, CompletedAt: inv.CompletedAt}
}

func (s *server) getPlatformTenantSelfInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	inv, ok := s.platformTenantSelfInvocation(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, platformTenantInvocationResponse(inv))
}

func (s *server) cancelPlatformTenantSelfInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	inv, ok := s.platformTenantSelfInvocation(w, r, acct)
	if !ok {
		return
	}
	if err := s.store.CancelInvocation(r.Context(), inv.ID); err != nil {
		api.WriteProblem(w, api.ErrInternal("cancel tenant invocation"))
		return
	}
	inv, err := s.store.InvocationByID(r.Context(), inv.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("read cancelled invocation"))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantInvocationResponse(inv))
}

func (s *server) replayPlatformTenantSelfInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orig, ok := s.platformTenantSelfInvocation(w, r, acct)
	if !ok {
		return
	}
	if problem := plainReplayEligibility(orig); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.servePlainReplay(w, r, acct, orig, orig.RetryPolicyJSON, "/v1/platform-tenant-self/invocations/")
}
