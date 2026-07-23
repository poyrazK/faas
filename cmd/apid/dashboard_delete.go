package main

// G6 dashboard delete/restore forms (spec §17 G6, ADR-021, security
// review A3).
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
// CSRF defence (review finding A3): the form posts a sealed
// envelope bound to (action, account_id) that the shared
// middleware.VerifyAuthenticated helper verifies. The renderer
// (renderAccount in handlers_dashboard.go) mints the token at GET
// time using middleware.IssueForAuthenticated and sets it both as
// the faas_csrf sidecar cookie and as the form's csrf_token hidden
// field. A cross-site POST cannot read the cookie, so the helper
// rejects before any state change.

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardDelete handles POST /dashboard/account/delete. The form
// posts here with a sealed csrf_token; we verify it against the
// faas_csrf cookie + account binding and call scheduleDeletion.
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
	if err := middleware.VerifyAuthenticated(s.sessions, r, "delete", acct.ID); err != nil {
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
// dashboardDelete — verify the csrf_token against (action="restore",
// account_id), call cancelDeletion, redirect to the success banner.
func (s *server) dashboardRestore(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, "restore", acct.ID); err != nil {
		http.Error(w, "invalid confirmation token", http.StatusBadRequest)
		return
	}
	if _, prob := s.cancelDeletion(r.Context(), acct); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	http.Redirect(w, r, "/dashboard/account?restored=1", http.StatusFound)
}

// dashboardExport handles GET /dashboard/account/export. The dashboard
// template's "Download JSON export" link previously pointed at
// /v1/account/export, which sits behind s.auth (Bearer API key) —
// the dashboard only has the session cookie, so the link silently 401'd.
// This handler serves the same JSON bundle from a session-authenticated
// route, reusing gatherExport so the wire shape stays identical to the
// REST export.
//
// Like the REST endpoint, this is recorded in the gdpr_requests
// audit ledger (PR #83 review #5) so a customer browsing the
// dashboard sees the same audit trail as one using the CLI. Set
// X-Audit-Logged: false on the response if the audit INSERT failed
// so DevTools-flag-reading tooling can detect the degraded state.
func (s *server) dashboardExport(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Mirror the REST endpoint's ?include_secrets=false flag.
	include := r.URL.Query().Get("include_secrets") != "false"
	bundle, err := gatherExport(r.Context(), s, acct, include)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not assemble export"))
		return
	}
	if !s.recordGdprRequest(r.Context(), acct, state.GdprActionExport) {
		w.Header().Set("X-Audit-Logged", "false")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		`attachment; filename="faas-account-`+acct.ID+`-`+
			time.Now().UTC().Format("20060102")+`.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(bundle)
}

// dashboardDPA handles GET /dashboard/account/dpa. Renders the DPA
// markdown into the dashboard chrome (vs the public /v1/account/dpa
// which streams the file raw). Same source, different envelope: a
// customer signed in expects the dashboard layout, not a raw
// markdown text body. The route is session-authed because the DPA
// references the customer's data posture and is more useful in
// context (no prospect browsing).
//
// On misconfiguration (FAAS_DPA_PATH unset) the same 503 the public
// route emits is surfaced — a customer sees "the operator hasn't
// installed the DPA yet, contact support" rather than a half-rendered
// page.
func (s *server) dashboardDPA(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.dpaPath == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable,
			api.CodeCapacity, "DPA template unavailable",
			"the DPA is not installed on this host; contact support"))
		return
	}
	body, err := os.ReadFile(s.dpaPath)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("DPA template unavailable"))
		return
	}
	page := dashboard.Page{
		Title:   "Data Processing Agreement",
		Account: acctViewFrom(acct),
		Body:    "dpa",
		Data: dashboard.DPAView{
			Markdown: string(body),
		},
	}
	if err := dashboard.Render(w, s.log, page); err != nil {
		s.log.Error("dashboard: dpa render failed", "err", err)
	}
}

// acctViewFrom converts a state.Account to a dashboard.AccountView
// (the dashboard layer doesn't import pkg/state). Mirrors the
// conversion used by handlers_dashboard.go so the DPA page looks
// identical to the rest of the dashboard chrome.
func acctViewFrom(acct state.Account) *dashboard.AccountView {
	return &dashboard.AccountView{
		ID:    acct.ID,
		Email: acct.Email,
		Plan:  string(acct.Plan),
	}
}
