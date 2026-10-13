package apidserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) resolveDashboardSavedView(w http.ResponseWriter, r *http.Request, app state.App, data *dashboardIssuesData) bool {
	key := r.URL.Query().Get("view")
	if key == "" {
		return true
	}
	st, ok := s.store.(state.IssueSavedViewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved issue views are unavailable"))
		return false
	}
	view, err := st.GetIssueSavedView(r.Context(), app.ID, app.AccountID, key)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	query := r.URL.Query()
	for key, values := range view.Filters.Query() {
		query[key] = values
	}
	r.URL.RawQuery = query.Encode()
	data.SavedViewKey = key
	return true
}

func (s *server) populateDashboardSavedViews(w http.ResponseWriter, r *http.Request, app state.App, data *dashboardIssuesData) bool {
	st, ok := s.store.(state.IssueSavedViewStore)
	if !ok {
		return true
	}
	views, err := st.ListIssueSavedViews(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeIssueError(w, err)
		return false
	}
	minimum, _ := strconv.ParseInt(data.MinCustomersFilter, 10, 64)
	data.SavedViews = &dashboard.IssueSavedViewsView{AppSlug: data.AppSlug, CSRF: data.CSRF, CurrentKey: data.SavedViewKey, CurrentFilters: api.IssueSavedViewFilters{Team: data.TeamFilter, State: data.StateFilter, Environment: data.EnvironmentFilter, Assignee: data.AssigneeFilter, Sort: data.SortFilter, MinCustomers: minimum, Tag: data.TagFilter}, Items: dashboard.IssueSavedViewLinks(data.AppSlug, views.Items), MaxViews: api.IssueSavedViewsMax, Updated: r.URL.Query().Get("saved_view") == "updated"}
	return true
}

func (s *server) dashboardIssueSavedViewHandler(w http.ResponseWriter, r *http.Request) {
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
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardIssueSavedViewForAccount)))(w, r)
}

func (s *server) dashboardIssueSavedViewForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.issueSavedViewsAccess(w, r, acct)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid saved view form"))
		return
	}
	key := r.PostForm.Get("key")
	if r.PostForm.Get("action") == "delete" {
		if err := st.DeleteIssueSavedView(r.Context(), app.ID, app.AccountID, key); err != nil {
			writeIssueError(w, err)
			return
		}
	} else if r.PostForm.Get("action") == "save" {
		in, ok := decodeDashboardSavedView(w, r)
		if !ok {
			return
		}
		if _, err := st.PutIssueSavedView(r.Context(), app.ID, app.AccountID, acct.ID, key, in, time.Now().UTC()); err != nil {
			writeIssueError(w, err)
			return
		}
	} else {
		api.WriteProblem(w, api.ErrValidation("unknown saved view action"))
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+app.Slug+"/issues?saved_view=updated", http.StatusSeeOther)
}

func decodeDashboardSavedView(w http.ResponseWriter, r *http.Request) (api.PutIssueSavedViewRequest, bool) {
	minimum, err := strconv.ParseInt(r.PostForm.Get("min_customers"), 10, 64)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid customer threshold"))
		return api.PutIssueSavedViewRequest{}, false
	}
	in := api.PutIssueSavedViewRequest{Name: r.PostForm.Get("name"), Filters: api.IssueSavedViewFilters{Team: r.PostForm.Get("team"), State: r.PostForm.Get("state"), Environment: r.PostForm.Get("environment"), Assignee: r.PostForm.Get("assignee"), Sort: r.PostForm.Get("sort"), MinCustomers: minimum, Tag: r.PostForm.Get("tag")}}
	in, err = api.NormalizeIssueSavedView(in)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return in, false
	}
	return in, true
}
