package apidserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type dashboardIssueLifecycleAlertsData struct {
	AppSlug, CSRF, EnvironmentsText, IssueID, Event string
	Policy                                          api.IssueLifecycleAlertPolicy
	Preview                                         *api.IssueLifecycleAlertPreview
	Saved, Draft                                    bool
	MaxEnvironments, MaxCooldown                    int
}

func (s *server) dashboardIssueLifecycleAlerts(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
		if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload the lifecycle alert page"))
			return
		}
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueLifecycleAlertsForAccount)))(w, r)
}

func (s *server) dashboardIssueLifecycleAlertsForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueLifecycleAccess(w, r, acct)
	if !ok {
		return
	}
	policy, err := st.GetIssueLifecycleAlertPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	view := dashboardIssueLifecycleAlertsData{AppSlug: app.Slug, Policy: policy, EnvironmentsText: strings.Join(policy.Environments, ", "), Event: "regressed", Saved: r.URL.Query().Get("saved") == "1", MaxEnvironments: api.IssueLifecycleAlertMaxEnvironments, MaxCooldown: api.IssueLifecycleAlertMaxCooldownSeconds}
	if r.Method == http.MethodPost && !s.dashboardIssueLifecycleDraft(w, r, st, app, &view) {
		return
	}
	s.renderIssueLifecycleAlerts(w, r, acct, view)
}

func lifecycleAlertForm(form url.Values) (api.UpdateIssueLifecycleAlertPolicyRequest, error) {
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return api.UpdateIssueLifecycleAlertPolicyRequest{}, fmt.Errorf("invalid policy revision; reload the page")
	}
	cooldown, err := strconv.ParseInt(form.Get("cooldown_seconds"), 10, 64)
	if err != nil {
		return api.UpdateIssueLifecycleAlertPolicyRequest{}, fmt.Errorf("cooldown must be a whole number of seconds")
	}
	environments := []string{}
	if text := strings.TrimSpace(form.Get("environments")); text != "" {
		for _, name := range strings.Split(text, ",") {
			environments = append(environments, strings.TrimSpace(name))
		}
	}
	environments, err = api.NormalizeIssueLifecycleAlertPolicy(environments, cooldown)
	return api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &revision, Environments: environments, CooldownSeconds: cooldown}, err
}

func (s *server) dashboardIssueLifecycleDraft(w http.ResponseWriter, r *http.Request, st state.IssueLifecycleAlertStore, app state.App, view *dashboardIssueLifecycleAlertsData) bool {
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid lifecycle alert form"))
		return false
	}
	in, err := lifecycleAlertForm(r.PostForm)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return false
	}
	switch r.PostForm.Get("action") {
	case "save":
		_, err = st.SetIssueLifecycleAlertPolicy(r.Context(), app.ID, app.AccountID, in)
		if err == nil {
			http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/lifecycle-alerts?saved=1", http.StatusSeeOther)
			return false
		}
	case "preview":
		view.Policy = api.IssueLifecycleAlertPolicy{Revision: *in.ExpectedRevision, Environments: in.Environments, CooldownSeconds: in.CooldownSeconds}
		view.EnvironmentsText, view.IssueID, view.Event = strings.Join(in.Environments, ", "), r.PostForm.Get("issue_id"), r.PostForm.Get("event")
		preview, e := st.PreviewIssueLifecycleAlert(r.Context(), app.ID, app.AccountID, api.PreviewIssueLifecycleAlertRequest{IssueID: view.IssueID, Event: view.Event, Environments: in.Environments, CooldownSeconds: in.CooldownSeconds}, time.Now().UTC())
		err = e
		view.Preview, view.Draft = &preview, true
	default:
		err = state.ErrInvalidArgument
	}
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	return true
}

func (s *server) renderIssueLifecycleAlerts(w http.ResponseWriter, r *http.Request, acct state.Account, view dashboardIssueLifecycleAlertsData) {
	csrf, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	view.CSRF = csrf
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: csrf, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Lifecycle alerts", Body: "issue_lifecycle_alerts", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
