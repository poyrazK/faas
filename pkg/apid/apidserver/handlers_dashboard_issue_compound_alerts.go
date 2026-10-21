package apidserver

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuealerts"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type dashboardIssueCompoundAlertsData struct {
	AppSlug, CSRF, IssueID                                                            string
	Policy                                                                            api.IssueCompoundAlertPolicy
	Draft                                                                             api.IssueCompoundAlertRule
	Preview                                                                           *api.IssueCompoundAlertPreview
	Saved                                                                             bool
	MaxRules, MaxEvents, MaxCustomers, MinWindow, MaxWindow, MinCooldown, MaxCooldown int
}

func (s *server) dashboardIssueCompoundAlerts(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
		if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload compound alerts"))
			return
		}
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueCompoundAlertsForAccount)))(w, r)
}

func (s *server) dashboardIssueCompoundAlertsForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueCompoundAccess(w, r, acct)
	if !ok {
		return
	}
	policy, err := st.GetIssueCompoundAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	view := dashboardIssueCompoundAlertsData{AppSlug: app.Slug, Policy: policy, Saved: r.URL.Query().Get("saved") == "1",
		Draft:    api.IssueCompoundAlertRule{Enabled: true, MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: api.IssueFrequencyAlertMinWindowSeconds, CooldownSeconds: api.IssueFrequencyAlertMinCooldownSeconds},
		MaxRules: api.IssueCompoundAlertMaxRules, MaxEvents: api.IssueFrequencyAlertMaxEvents, MaxCustomers: api.IssueCompoundAlertMaxCustomers,
		MinWindow: api.IssueFrequencyAlertMinWindowSeconds, MaxWindow: api.IssueFrequencyAlertMaxWindowSeconds, MinCooldown: api.IssueFrequencyAlertMinCooldownSeconds, MaxCooldown: api.IssueFrequencyAlertMaxCooldownSeconds}
	if r.Method == http.MethodPost && !s.dashboardIssueCompoundDraft(w, r, st, app, &view) {
		return
	}
	s.renderIssueCompoundAlerts(w, r, acct, view)
}

func (s *server) dashboardIssueCompoundDraft(w http.ResponseWriter, r *http.Request, st state.IssueCompoundAlertStore, app state.App, view *dashboardIssueCompoundAlertsData) bool {
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid compound alert form"))
		return false
	}
	if r.PostForm.Get("action") == "preview" {
		rule, err := issuealerts.CompoundRuleForm(r.PostForm)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return false
		}
		view.Draft, view.IssueID = rule, r.PostForm.Get("issue_id")
		preview, err := st.PreviewIssueCompoundAlert(r.Context(), app.ID, app.AccountID, api.PreviewIssueCompoundAlertRequest{IssueID: view.IssueID, Rule: rule}, time.Now().UTC())
		if err != nil {
			writeIssueError(w, err)
			return false
		}
		view.Preview = &preview
		return true
	}
	in, err := issuealerts.CompoundFormUpdate(view.Policy, r.PostForm)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return false
	}
	_, err = st.SetIssueCompoundAlertPolicy(r.Context(), app.ID, app.AccountID, in)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/compound-alerts?saved=1", http.StatusSeeOther)
	return false
}

func (s *server) renderIssueCompoundAlerts(w http.ResponseWriter, r *http.Request, acct state.Account, view dashboardIssueCompoundAlertsData) {
	csrf, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	view.CSRF = csrf
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: csrf, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Compound alerts", Body: "issue_compound_alerts", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
