package apidserver

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardIssueTrackerReviewHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the owner may review tracker sync"))
		return
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return
	}
	store, ok := s.store.(state.IssueTrackerReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("tracker review unavailable"))
		return
	}
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "all"
	}
	cursor, err := state.DecodeIssueCursor(r.URL.Query().Get("cursor"))
	if err != nil || !state.ValidIssueTrackerReviewCategory(category) {
		api.WriteProblem(w, api.ErrValidation("invalid review filter or cursor"))
		return
	}
	page, err := store.ListIssueTrackerReviews(r.Context(), app.ID, acct.ID, category, cursor)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	s.renderDashboardTrackerReviews(w, r, acct, app, category, page)
}

func (s *server) renderDashboardTrackerReviews(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, category string, page state.IssueTrackerReviewPage) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.IssueTrackerReviewView{AppSlug: app.Slug, Category: category, CSRF: token, Items: page.Items, Reasons: map[string]string{}, Recovery: map[string]string{}}
	for _, item := range page.Items {
		view.Reasons[item.LinkID] = dashboard.IssueTrackerSyncReason(item.Reason)
		view.Recovery[item.LinkID] = dashboard.IssueTrackerRecoverySummary(true, item.Outcome, item.PendingState)
	}
	if page.NextCursor != "" {
		view.NextURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/issues/tracker-review?category=" + url.QueryEscape(category) + "&cursor=" + url.QueryEscape(page.NextCursor)
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Tracker sync review", Body: "issue_tracker_review", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
