package apidserver

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardIssueSplitRuleHandler(w http.ResponseWriter, r *http.Request) {
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
	if strings.HasSuffix(r.URL.Path, "/undo") {
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueSplitUndoForAccount)))(w, r)
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueSplitRuleForAccount)))(w, r)
}

func (s *server) dashboardIssueSplitRuleForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid split rule form"))
		return
	}
	var rule api.IssueSplitRule
	var err error
	if r.PathValue("rule_id") != "" {
		enabled, parseErr := strconv.ParseBool(r.FormValue("enabled"))
		if parseErr != nil || !validIssueSplitRulePath(r) {
			api.WriteProblem(w, api.ErrValidation("invalid split rule state or identity"))
			return
		}
		rule, err = st.SetIssueSplitRuleEnabled(r.Context(), app.ID, r.PathValue("issue_id"), r.PathValue("rule_id"), acct.ID, enabled, time.Now().UTC())
	} else {
		matcher := api.IssueSplitMatcher{ExceptionType: r.FormValue("exception_type"), ErrorCode: r.FormValue("error_code"), Route: r.FormValue("route"), SourceKind: r.FormValue("source_kind")}
		if err = matcher.Validate(); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		rule, err = st.CreateIssueSplitRule(r.Context(), app.ID, r.PathValue("issue_id"), acct.ID, matcher, acct.Plan.IssueLimits(), time.Now().UTC())
	}
	if err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?issue="+url.QueryEscape(rule.TargetIssueID)+"&split_rule=updated", http.StatusSeeOther)
}
