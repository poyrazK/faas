package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardTrackerTriageHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	st, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	triage, err := st.GetIssueTrackerTriage(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("notification_id"))
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
	view := dashboard.NewIssueTrackerTriageView(app.Slug, token, triage, triage.ClaimedBy == acct.ID || app.AccountID == acct.ID)
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Incident triage", Body: "issue_tracker_triage", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardTrackerTriageActionHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload incident triage"))
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardTrackerTriageActionForAccount)))(w, r)
}

func (s *server) dashboardTrackerTriageActionForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid triage action"))
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("reload incident triage"))
		return
	}
	req, err := issuetrackers.ParseTriageAction(r.PostForm)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if err = st.UpdateIssueTrackerTriage(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("notification_id"), revision, req); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications/triage/"+url.PathEscape(r.PathValue("notification_id")), http.StatusSeeOther)
}
