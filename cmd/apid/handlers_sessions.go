// IAM-3 server-side session handlers (ADR-039, issue #187 + #244 merged).
//
// Four endpoints mounted under /v1/auth/* (registered in server.go):
//
//   - POST   /v1/auth/logout                — revoke this device.
//   - GET    /v1/auth/sessions              — list active sessions.
//   - DELETE /v1/auth/sessions/{id}         — revoke a sibling.
//   - POST   /v1/auth/sessions/revoke_all   — revoke every other device.
//
// Every handler reads the current session via sessionFrom(r) —
// stamped by the cookie branch of s.auth (server.go, the
// requireSessionCookie call). Each handler is ≤ 50 lines per
// CLAUDE.md "Handlers ≤ 50 lines" rule; the Table-driven tests in
// handlers_sessions_test.go exercise the whole surface.
//
// CSRF error code is the project-wide "csrf_mismatch" literal
// (matches the existing handlers_github.go / handlers_google.go
// convention; pkg/api.CodeCSRFInvalid was a stale identifier from
// an earlier draft and was removed when this PR landed).
package main

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// csrfActionLogout is the action-name stamped into the
	// double-submit CSRF token for POST /v1/auth/logout. Distinct
	// from the existing "delete" action so a CSRF minted for the
	// account-deletion form cannot be replayed against logout
	// and vice-versa.
	csrfActionLogout = "auth.logout"

	// csrfActionSessionRevoke is the action-name for DELETE
	// /v1/auth/sessions/{id}. The action binds the token to
	// "revoke exactly this sid" so a token minted for one
	// sibling cannot be replayed against another.
	csrfActionSessionRevoke = "auth.session.revoke"

	// csrfActionSessionsRevokeAll is the action-name for POST
	// /v1/auth/sessions/revoke_all.
	csrfActionSessionsRevokeAll = "auth.sessions.revoke_all"
)

// csrfMismatchProblem is the 400 response every CSRF failure
// funnels through. Uses the project-wide "csrf_mismatch" literal
// in lieu of a pkg/api constant so the wire shape stays
// identical to handlers_github.go / handlers_google.go without
// adding a new error code for one user.
var csrfMismatchProblem = api.NewProblem(http.StatusBadRequest, "csrf_mismatch",
	"CSRF Error", "the form's csrf_token does not match the bound action")

// logout revokes the calling sid (this device). Always returns
// 204 No Content on success — even when the row was already
// revoked (RevokeSession returns false on no-op), so the action
// is idempotent under retries. The cookie is cleared on every
// success path; the audit Emit fires only on a real write so
// repeated logout clicks from an already-cleared cookie stay
// quiet.
func (s *server) logout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, csrfActionLogout, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	current, ok := sessionFrom(r)
	if !ok {
		// No session in context — should be unreachable because
		// requireSessionCookie stamped it. Fail closed.
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeSessionExpired, "Session expired", "no active session in context"))
		return
	}
	if _, err := s.store.RevokeSession(r.Context(), current.ID, acct.ID); err != nil {
		writeCustomerInternalProblem(w, r, s.log, "revoke current dashboard session",
			"Gregale could not sign out this device.",
			"Retry signing out; if the session remains active, contact support.", err)
		return
	}
	s.clearSessionCookie(w, r)
	if s.audit != nil {
		s.audit.Emit(r.Context(), "auth.session.revoke", &acct.ID, map[string]any{
			"sid":    current.ID,
			"reason": "logout",
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSessions returns every active row for the calling account,
// newest first. The row whose id matches the calling cookie's sid
// is flagged current_session=true so the dashboard can render the
// "this device" pill. Empty list when the account has only the
// calling session (RevokeAllSessions would flip to zero after).
func (s *server) listSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sessions, err := s.store.ListSessions(r.Context(), acct.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list dashboard sessions",
			"Gregale could not load your active sessions.",
			"Reload the page in a moment; if it continues, contact support.", err)
		return
	}
	current, ok := sessionFrom(r)
	currentID := ""
	if ok {
		currentID = current.ID
	}
	out := api.SessionListResponse{Sessions: make([]api.SessionInfo, 0, len(sessions))}
	for _, sess := range sessions {
		out.Sessions = append(out.Sessions, api.SessionInfo{
			ID:             sess.ID,
			IssuedIP:       sess.IssuedIP,
			IssuedUA:       sess.IssuedUA,
			IssuedAt:       sess.IssuedAt.Format(time.RFC3339),
			LastSeenAt:     formatLastSeen(sess.LastSeenAt),
			CurrentSession: sess.ID == currentID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeSession deletes a sibling by id (or the calling id, in
// which case the calling cookie is also cleared). The CSRF action
// binds the token to "revoke exactly this sid"; a token minted
// against one sibling cannot be replayed against another.
//
// Cross-account DELETE returns 404 (we never confirm a row
// exists in another account). The SQL `account_id = $2` predicate
// is the load-bearing IDOR guard — handler logic is just the
// `false → 404` mapping.
func (s *server) revokeSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest,
			api.CodeValidation, "Invalid session id", "id must be a uuid"))
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, csrfActionSessionRevoke, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	ok, err := s.store.RevokeSession(r.Context(), id, acct.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "revoke dashboard session",
			"Gregale could not revoke this session.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
			api.CodeNotFound, "Session not found", "no session with that id for this account"))
		return
	}
	// Current session? Clear the cookie + 204 (consistent with
	// the logout handler — same "log out this device" path).
	current, hasCurrent := sessionFrom(r)
	if hasCurrent && current.ID == id {
		s.clearSessionCookie(w, r)
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "auth.session.revoke", &acct.ID, map[string]any{
			"sid":    id,
			"reason": "explicit",
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeAllSessions revokes every active row for accountID
// except the calling sid. Returns {"revoked": N}. Current
// session stays active — the caller keeps their cookie.
func (s *server) revokeAllSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, csrfActionSessionsRevokeAll, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	current, ok := sessionFrom(r)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeSessionExpired, "Session expired", "no active session in context"))
		return
	}
	n, err := s.store.RevokeAllSessions(r.Context(), acct.ID, current.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "revoke other dashboard sessions",
			"Gregale could not sign out your other devices.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "auth.sessions.revoke_all", &acct.ID, map[string]any{
			"revoked_count": n,
			"retained_sid":  current.ID,
		})
	}
	writeJSON(w, http.StatusOK, api.SessionsRevokeAllResponse{Revoked: n})
}

// formatLastSeen renders the last_seen_at column for the wire
// shape. Empty string when the row has never been touched (a
// freshly-minted session hasn't had a request hit it yet — the
// dashboard renders "fresh" rather than the umpteenth RFC3339
// string of the same UTC second).
func formatLastSeen(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
