package apidserver

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuealerts"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueFrequencyAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueFrequencyAlertStore, state.App, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, app, false
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return nil, app, false
	}
	st, ok := s.store.(state.IssueFrequencyAlertStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issue frequency alerts are unavailable"))
	}
	return st, app, ok
}
func (s *server) getIssueFrequencyAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueFrequencyAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := st.GetIssueFrequencyAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setIssueFrequencyAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueFrequencyAccess(w, r, acct)
	if !ok {
		return
	}
	var in api.UpdateIssueFrequencyAlertPolicyRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.Rules == nil {
		api.WriteProblem(w, api.ErrValidation("rules must be an array; use [] to remove all rules"))
		return
	}
	rules, err := api.NormalizeIssueFrequencyAlertRules(in.Rules)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	in.Rules = rules
	out, err := st.SetIssueFrequencyAlertPolicy(r.Context(), app.ID, app.AccountID, in)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) dashboardIssueFrequencyAlerts(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload the issues page"))
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueFrequencyAlertsForAccount)))(w, r)
}
func (s *server) dashboardIssueFrequencyAlertsForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueFrequencyAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid frequency alert form"))
		return
	}
	current, err := st.GetIssueFrequencyAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	in, err := issuealerts.FormUpdate(current, r.PostForm)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if _, err = st.SetIssueFrequencyAlertPolicy(r.Context(), app.ID, app.AccountID, in); err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?frequency_alert=updated", http.StatusSeeOther)
}
