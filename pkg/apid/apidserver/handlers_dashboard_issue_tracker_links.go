package apidserver

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) populateDashboardTrackerLinks(w http.ResponseWriter, r *http.Request, app state.App, data *dashboardIssuesData) bool {
	if data.Detail == nil {
		return true
	}
	st, ok := s.store.(state.IssueTrackerLinkStore)
	if !ok {
		return true
	}
	cur, err := state.DecodeIssueCursor(r.URL.Query().Get("tracker_link_cursor"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid tracker-link cursor"))
		return false
	}
	out, err := st.ListIssueTrackerLinks(r.Context(), app.ID, app.AccountID, data.Detail.Issue.ID, cur)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	view := &dashboard.IssueTrackerLinksView{AppSlug: app.Slug, IssueID: data.Detail.Issue.ID, CSRF: data.CSRF, Items: out.Items}
	if out.NextCursor != "" {
		query := url.Values{"issue": {view.IssueID}, "tracker_link_cursor": {out.NextCursor}, "since": {data.Since}}
		if data.SavedViewKey != "" {
			query.Set("view", data.SavedViewKey)
		}
		view.NextURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/issues?" + query.Encode()
	}
	if !s.populateDashboardTrackerSync(w, r, app, data.CurrentAccountID, view) {
		return false
	}
	data.TrackerLinks = view
	return true
}
func (s *server) dashboardIssueTrackerLinkHandler(w http.ResponseWriter, r *http.Request) {
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
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueTrackerLinkForAccount)))(w, r)
}
func (s *server) dashboardIssueTrackerLinkForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueTrackerLinksAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid tracker-link form"))
		return
	}
	id := r.PathValue("issue_id")
	var err error
	switch r.PostForm.Get("action") {
	case "delete":
		err = st.DeleteIssueTrackerLink(r.Context(), app.ID, app.AccountID, id, r.PostForm.Get("link_id"), acct.ID, time.Now().UTC())
	case "save":
		in := api.PutIssueTrackerLinkRequest{URL: r.PostForm.Get("url"), Title: r.PostForm.Get("title")}
		if _, e := api.NormalizeIssueTrackerLink(in); e != nil {
			api.WriteProblem(w, api.ErrValidation(e.Error()))
			return
		}
		_, err = st.PutIssueTrackerLink(r.Context(), app.ID, app.AccountID, id, acct.ID, in, time.Now().UTC())
	default:
		api.WriteProblem(w, api.ErrValidation("unknown tracker-link action"))
		return
	}
	if err != nil {
		writeIssueError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues?issue="+url.QueryEscape(id), http.StatusSeeOther)
}
