package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardIssueAction = "issue_action"
const dashboardIssueCookie = "faas_csrf_issue_action"

type dashboardIssuesData struct {
	AppSlug     string
	Items       []api.Issue
	Detail      *api.IssueDetail
	CSRF        string
	Error       string
	NextURL     string
	Members     []dashboardIssueMember
	Deployments []state.Deployment
}

func parseAppIssuesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	if !strings.HasSuffix(rest, "/issues") {
		return "", false
	}
	slug := strings.TrimSuffix(rest, "/issues")
	return slug, validSlug(slug) && !strings.Contains(slug, "/")
}
func (s *server) renderAppIssues(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	data := dashboardIssuesData{AppSlug: slug}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	data.CSRF = token
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	if !populateIssueDashboardList(w, r, app, st, &data) || !populateIssueDashboardDetail(w, r, app, acct, st, &data) {
		return
	}
	s.populateIssueDashboardChoices(r.Context(), app, acct, &data)
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: slug + " issues", Body: "issues", Account: dashboardAccountView(acct, count), Data: data}); err != nil {
		renderProblem(w, log, err)
	}
}
func (s *server) dashboardIssueActionHandler(w http.ResponseWriter, r *http.Request) {
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
	// Session principal authorization remains identical to the API write surface.
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueActionForAccount)))(w, r)
}
func (s *server) dashboardIssueActionForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("issue_id")); err != nil {
		http.NotFound(w, r)
		return
	}
	in, err := decodeDashboardIssueAction(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := validateIssueAction(in, time.Now()); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if _, err := st.ActOnIssue(r.Context(), app.ID, r.PathValue("issue_id"), acct.ID, in, time.Now().UTC()); err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?issue="+url.QueryEscape(r.PathValue("issue_id")), http.StatusSeeOther)
}

type dashboardIssueMember struct{ ID, Name string }

func populateIssueDashboardList(w http.ResponseWriter, r *http.Request, app state.App, st state.IssueStore, data *dashboardIssuesData) bool {
	slug := app.Slug
	cur, err := state.DecodeIssueCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid issue cursor"))
		return false
	}
	list, err := st.ListIssues(r.Context(), app.ID, r.URL.Query().Get("state"), r.URL.Query().Get("environment"), cur)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	data.Items = list.Items
	if list.NextCursor != "" {
		q := r.URL.Query()
		q.Set("cursor", list.NextCursor)
		data.NextURL = "/dashboard/apps/" + url.PathEscape(slug) + "/issues?" + q.Encode()
	}
	return true
}

func populateIssueDashboardDetail(w http.ResponseWriter, r *http.Request, app state.App, acct state.Account, st state.IssueStore, data *dashboardIssuesData) bool {
	if id := r.URL.Query().Get("issue"); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			http.NotFound(w, r)
			return false
		}
		since, until, err := issueWindow(r, acct.Plan)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return false
		}
		eventCur, err := issueDetailCursors(r)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid event cursor"))
			return false
		}
		detail, err := st.GetIssueDetail(r.Context(), app.ID, id, since, until, eventCur)
		if err != nil {
			writeIssueError(w, err)
			return false
		}
		data.Detail = &detail
	}
	return true
}

func (s *server) populateIssueDashboardChoices(ctx context.Context, app state.App, acct state.Account, data *dashboardIssuesData) {
	if app.OrgID != "" {
		members, _ := s.store.ListOrgMembers(ctx, app.OrgID)
		for _, member := range members {
			if member.RemovedAt == nil {
				if a, err := s.store.AccountByID(ctx, member.AccountID); err == nil {
					data.Members = append(data.Members, dashboardIssueMember{ID: a.ID, Name: a.Email})
				}
			}
		}
	}
	if len(data.Members) == 0 {
		data.Members = []dashboardIssueMember{{ID: app.AccountID, Name: acct.Email}}
	}
	data.Deployments, _ = s.store.ListDeploymentsForApp(ctx, app.ID, api.IssuePageSize, 0)
}

func decodeDashboardIssueAction(r *http.Request) (api.IssueActionRequest, error) {
	if err := r.ParseForm(); err != nil {
		return api.IssueActionRequest{}, fmt.Errorf("invalid issue form: %w", err)
	}
	in := api.IssueActionRequest{Action: r.FormValue("action"), AssigneeAccountID: r.FormValue("assignee_account_id"), FixedDeploymentID: r.FormValue("fixed_deployment_id")}
	if raw := r.FormValue("ignored_until"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return in, fmt.Errorf("ignored_until must be RFC3339: %w", err)
		}
		in.IgnoredUntil = &v
	}
	return in, nil
}
