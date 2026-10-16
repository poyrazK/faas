package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardApplicationTriageAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueApplicationTriageStore, state.App, bool) {
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return nil, app, false
	}
	st, ok := s.store.(state.IssueApplicationTriageStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue response unavailable"))
		return nil, app, false
	}
	// The store checks owner entitlement and current responder membership on every read and write.
	return st, app, true
}
func (s *server) dashboardApplicationTriageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	st, app, ok := s.dashboardApplicationTriageAccess(w, r, acct)
	if !ok {
		return
	}
	triage, err := st.GetIssueApplicationTriage(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("issue_id"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.NewIssueApplicationTriageView(app.Slug, token, triage, acct.ID == app.AccountID)
	if !s.dashboardSlackAccountLinkView(w, r, app, acct.ID, &view) {
		return
	}
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Issue response", Body: "issue_application_triage", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) dashboardApplicationTriageActionHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload issue response"))
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardApplicationTriageActionForAccount)))(w, r)
}
func (s *server) dashboardApplicationTriageActionForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.dashboardApplicationTriageAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeIssueError(w, state.ErrInvalidArgument)
		return
	}
	if s.dashboardSlackAccountLinkAction(w, r, acct, app) {
		return
	}
	revision, req, err := issuetriage.ParseAction(r.PostForm)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	target, err := applicationResponseRedirect(r, app.Slug)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if err = st.UpdateIssueApplicationTriage(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("issue_id"), revision, req); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
