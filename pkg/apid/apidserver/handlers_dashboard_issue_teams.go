package apidserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardIssueTeamHandler(w http.ResponseWriter, r *http.Request) {
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
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueTeamForAccount)))(w, r)
}

func (s *server) dashboardIssueTeamForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTeamsAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid team form"))
		return
	}
	key := r.PostForm.Get("key")
	if r.PostForm.Get("action") == "delete" {
		if err := st.DeleteIssueTeam(r.Context(), app.ID, app.AccountID, key); err != nil {
			writeIssueError(w, err)
			return
		}
	} else if r.PostForm.Get("action") == "save" {
		in, err := api.NormalizeIssueTeam(api.PutIssueTeamRequest{Name: r.PostForm.Get("name"), MemberAccountIDs: strings.Split(r.PostForm.Get("members"), ",")})
		if err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
		if _, err := st.PutIssueTeam(r.Context(), app.ID, app.AccountID, acct.ID, key, in, time.Now().UTC()); err != nil {
			writeIssueError(w, err)
			return
		}
	} else {
		api.WriteProblem(w, api.ErrValidation("unknown team action"))
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+app.Slug+"/issues?team_updated=1", http.StatusSeeOther)
}
