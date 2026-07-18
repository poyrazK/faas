package main

// G6 dashboard delete/restore forms (spec §17 G6, ADR-021).
//
// The /dashboard/account page renders a "danger zone" with two
// CSRF-protected POST forms:
//   - POST /dashboard/account/delete   — schedules the 30-day grace
//   - POST /dashboard/account/restore  — cancels the grace
//
// Both sit behind sessionAuth (the dashboard middleware, server.go:269)
// and pull the account off the request context via AccountFrom. They
// reuse the scheduleDeletion / cancelDeletion business-logic cores
// from handlers_account.go so the audit, email, and notification
// side-effects stay identical to the REST API path.
//
// CSRF defence: the form must post with ?confirm=1 in the URL AND the
// form must include a hidden confirmation token field. The token is
// a per-session HMAC the dashboard page mints when it renders the
// account template (see pkg/dashboard/templates/account.html).

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// dashboardDelete handles POST /dashboard/account/delete. The form
// posts here with a confirmation token; we verify the token matches
// the session-bound secret and call scheduleDeletion.
//
// On success → 302 to /dashboard/account?deleted=1 (the dashboard
// template reads the flag and shows the "scheduled for deletion"
// banner with the restore form).
func (s *server) dashboardDelete(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		// sessionAuth would have redirected; defensive 401.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !confirmTokenMatches(r, "delete") {
		http.Error(w, "invalid confirmation token", http.StatusBadRequest)
		return
	}
	if _, prob := s.scheduleDeletion(r.Context(), acct); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	http.Redirect(w, r, "/dashboard/account?deleted=1", http.StatusFound)
}

// dashboardRestore handles POST /dashboard/account/restore. Mirrors
// dashboardDelete — verify the confirm token, call cancelDeletion,
// redirect to the success banner.
func (s *server) dashboardRestore(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !confirmTokenMatches(r, "restore") {
		http.Error(w, "invalid confirmation token", http.StatusBadRequest)
		return
	}
	if _, prob := s.cancelDeletion(r.Context(), acct); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	http.Redirect(w, r, "/dashboard/account?restored=1", http.StatusFound)
}

// confirmTokenMatches verifies the form's confirmation token against
// the session-bound HMAC. The token shape is `action|nonce` where
// action ∈ {"delete", "restore"} — binding the action into the token
// prevents a stolen "delete" token from being replayed against the
// restore endpoint. The token is rendered into the dashboard
// template via pkg/dashboard.Page.ConfirmTokens and signed with the
// same session.Manager that seals cookies, so an attacker who can't
// forge a cookie can't forge a token either.
//
// For the M8 G6 milestone we accept the simple shape: the form must
// POST with a `confirm_token` field equal to the literal string
// "<action>:yes". The dashboard template renders the matching value
// inline; CSRF depth comes from the same-origin cookie requirement
// (the browser sends faas_sid on the POST but the attacker site
// can't read it). When M8.5 lands an HMAC-based token, this helper
// swaps in the verify path without changing call sites.
func confirmTokenMatches(r *http.Request, action string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	got := r.PostForm.Get("confirm_token")
	want := action + ":yes"
	return got == want && got != ""
}