package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// renderAppLogSearchDashboard serves GET /dashboard/apps/{slug}/log-search
// (ADR-20261018083536): the log search form, facet counts, and one page of
// matching events.
func (s *server) renderAppLogSearchDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	data := s.logSearch.DashboardData(r.Context(), acct, app, r.URL.Query())
	page := dashboard.Page{Title: "Log search", Body: "app_log_search", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
