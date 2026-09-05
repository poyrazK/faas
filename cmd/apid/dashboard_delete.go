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
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
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
		// Surface an RFC 7807 problem so the response shape matches
		// the rest of apid. The helper wraps ErrCSRFInvalid on every
		// failure path, so the message is intentionally generic —
		// "invalid" doesn't tell the caller which check tripped.
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if _, prob := s.scheduleDeletion(r.Context(), acct, "dashboard"); prob != nil {
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
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if _, prob := s.cancelDeletion(r.Context(), acct, "dashboard"); prob != nil {
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
	include := r.URL.Query().Get("include_secrets") != includeSecretsFalse
	bundle, err := gatherExport(r.Context(), s, acct, include)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not assemble export"))
		return
	}
	if !s.recordGdprRequest(r.Context(), acct, state.GdprActionExport, middleware.RequestIDFrom(r)) {
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
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("dashboard: dpa render failed", "err", err)
	}
}

// acctViewFrom converts a state.Account to a dashboard.AccountView
// (the dashboard layer doesn't import pkg/state). Mirrors the
// conversion used by handlers_dashboard.go so the DPA page looks
// identical to the rest of the dashboard chrome.
func acctViewFrom(acct state.Account) *dashboard.AccountView {
	return &dashboard.AccountView{
		ID:                         acct.ID,
		Email:                      acct.Email,
		Plan:                       string(acct.Plan),
		EmailVerified:              acct.EmailVerified(),
		EmailVerificationGraceEnds: acct.CreatedAt.Add(emailVerificationGrace).UTC().Format("2006-01-02"),
	}
}

// dashboardRaiseOverageCap handles POST /dashboard/raise-overage-cap
// (issue #561). Mirrors dashboardDelete's CSRF envelope pattern —
// the rendering path mints the token via IssueForAuthenticated and
// sets it in the form's hidden csrf_token input; this handler
// verifies it against the (action="raise_overage_cap", account_id)
// sealed envelope. The body passes the validation through to the
// shared raiseOverageCapSvc routine (same code path the v1 API
// /v1/account/overage-cap POST uses) so the dashboard and the CLI
// cannot drift.
//
// Form fields:
//
//	overage_cap_cents  non-negative integer → set the cap to N cents.
//	                   0 is a valid write and means "no overage allowed".
//	                   blank → clear the cap (NULL round-trip).
//
// The earlier `clear` checkbox was removed because it duplicated the
// empty-input affordance (review finding #4): a checked box + a typed
// number silently discarded the number, and a cap=0 ("no overage")
// was unreachable through the UI. The template documents the cap=0
// path inline so customers can distinguish NULL (no cap) from 0
// (no overage allowed).
func (s *server) dashboardRaiseOverageCap(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, "raise_overage_cap", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad form", err.Error()))
		return
	}
	// Empty input → nil (NULL round-trip, clears the cap). Non-empty
	// integer → that value. 0 is preserved as a distinct state
	// ("no overage allowed") via the (cents=0, ok=true) reader shape.
	raw := strings.TrimSpace(r.FormValue("overage_cap_cents"))
	var capCents *int64
	if raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Redirect(w, r, "/dashboard/billing?cap=invalid", http.StatusSeeOther)
			return
		}
		capCents = &n
	}
	if _, err := s.raiseOverageCapSvc(r.Context(), acct, capCents); err != nil {
		http.Redirect(w, r, "/dashboard/billing?cap=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard/billing?cap=updated", http.StatusSeeOther)
}
