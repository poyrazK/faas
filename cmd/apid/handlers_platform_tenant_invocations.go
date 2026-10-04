package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
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
	if orig.State != state.InvocationFailed && orig.State != state.InvocationDeadLetter {
		api.WriteProblem(w, api.ErrInvocationNotReplayable(string(orig.State)))
		return
	}
	if problem := genericReplayPolicyProblem(orig); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	inv, err := s.enqueuePlatformTenantReplay(r.Context(), acct, orig, r.Header.Get("Idempotency-Key"))
	if errors.Is(err, state.ErrPlatformTenantSuspended) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Platform tenant suspended", "resume this customer before replaying work"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("replay tenant invocation"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{ID: inv.ID,
		StatusURL: "/v1/platform-tenant-self/invocations/" + inv.ID})
}

func (s *server) enqueuePlatformTenantReplay(ctx context.Context, acct state.Account, orig state.Invocation, idempotencyKey string) (state.Invocation, error) {
	inv := state.Invocation{AppID: orig.AppID, AccountID: acct.ID, PlatformTenantID: orig.PlatformTenantID,
		Source: state.InvocationReplay, Method: orig.Method, Path: orig.Path, Payload: orig.Payload,
		Headers: orig.Headers, DueAt: time.Now().UTC(), RetryPolicyJSON: orig.RetryPolicyJSON,
		DeadlineAt: deadlineForRequest(nil, acct), ResultRetentionUntil: retentionForRequest(nil, acct)}
	// Ownership is checked before the idempotency lookup; account-wide response
	// caching would disclose a customer's replay to another tenant on this path.
	if key := idempotencyKey; key != "" {
		inv.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/tenant-replay/v1\x00"+orig.PlatformTenantID+"\x00"+orig.ID+"\x00"+key)).String()
	}
	prepared, _, err := state.ResolveInvocationVersion(ctx, s.store, inv)
	if err == nil {
		inv, err = s.store.EnqueueInvocation(ctx, prepared)
	}
	if errors.Is(err, state.ErrConflict) && prepared.ID != "" {
		inv, err = s.store.InvocationByID(ctx, prepared.ID)
		if inv.PlatformTenantID != orig.PlatformTenantID || inv.AppID != orig.AppID || inv.AccountID != acct.ID {
			err = state.ErrConflict
		}
	}
	return inv, err
}
