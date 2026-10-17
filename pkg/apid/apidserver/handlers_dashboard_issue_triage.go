package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) populateIssueReleaseComparison(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, data *dashboardIssuesData) bool {
	data.BaselineDeployment = r.URL.Query().Get("baseline")
	data.CandidateDeployment = r.URL.Query().Get("candidate")
	if data.BaselineDeployment == "" && data.CandidateDeployment == "" {
		return true
	}
	st, ok := s.store.(state.IssueTriageStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release comparison is unavailable"))
		return false
	}
	env := data.EnvironmentFilter
	if env == "" {
		env = "application"
	}
	out, err := st.CompareIssueReleases(r.Context(), acct.ID, app.ID, data.BaselineDeployment, data.CandidateDeployment, env, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	data.ReleaseComparison = dashboard.IssueReleaseComparisonPresentation(out)
	return true
}

func (s *server) dashboardIssueBulkActionHandler(w http.ResponseWriter, r *http.Request) {
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
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueBulkActionForAccount)))(w, r)
}

func (s *server) dashboardIssueBulkActionForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTriageAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid bulk action form"))
		return
	}
	in := api.BulkIssueActionRequest{IssueIDs: r.PostForm["issue_ids"], Action: api.IssueActionRequest{Action: r.FormValue("action")}}
	switch in.Action.Action {
	case "assign":
		in.Action.AssigneeAccountID = r.FormValue("assignee_account_id")
		in.Action.AssigneeTeamID = r.FormValue("assignee_team_id")
	case "resolve":
		in.Action.FixedDeploymentID = r.FormValue("fixed_deployment_id")
	case "ignore":
		until, err := time.Parse(time.RFC3339, r.FormValue("ignored_until"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("ignored_until must be RFC3339"))
			return
		}
		in.Action.IgnoredUntil = &until
	}
	now := time.Now().UTC()
	in, err := api.NormalizeBulkIssueAction(in, now)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if _, err = st.BulkActOnIssues(r.Context(), app, acct.ID, in, now); err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+r.PathValue("slug")+"/issues?bulk_action=updated", http.StatusSeeOther)
}
