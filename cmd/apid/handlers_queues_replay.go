package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// queueDeadLetterReplay (ADR-134 PR-C) handles
// POST /v1/apps/{slug}/queues/dead_letter/{id}/replay.
//
// Resets an invocations row in state='dead_letter' back to
// 'pending' with attempts=0, last_error cleared, due_at=NOW(),
// last_replayed_at stamped. The same row is replayed — distinct
// from /v1/invocations/{id}/replay which enqueues a NEW row
// tagged Source=InvocationReplay. Direct mutation keeps the
// parent row's id intact so the dashboard's history view shows
// the replay chain on one row.
//
// IDOR-safe: accountID is read from the auth-scoped acct
// parameter (the route's authLimited wrapper has already resolved
// the caller). The store method takes accountID as an argument
// and returns ErrNotFound on mismatch — never 403 — so a
// cross-tenant probe does not leak the existence of the row.
//
// Idempotent at the store level: a second POST after the first
// has succeeded finds the row in 'pending' (not 'dead_letter')
// and returns ErrNotFound. The wrapper-level idempotent
// middleware keys on Idempotency-Key for double-POST safety
// across network retries.
func (s *server) queueDeadLetterReplay(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.RetryQueueDeadLetter(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// The row is missing, owned by another account, or
			// not in dead_letter (already replayed, or never
			// dead-lettered). All three render as 404 with the
			// same shape — no enumeration.
			api.WriteProblem(w, api.ErrInvocationNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("queue dead-letter replay"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{
		ID:        inv.ID,
		StatusURL: "/v1/invocations/" + inv.ID,
	})
}
