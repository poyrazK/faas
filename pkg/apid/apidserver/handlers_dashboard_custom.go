package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderCustomDashboards serves GET /dashboard/dashboards
// (ADR-20261022173833): the account's saved dashboards.
func (s *server) renderCustomDashboards(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data := dashboard.CustomDashboardsData{Enabled: s.dashboards.Enabled}
	if data.Enabled {
		list, err := s.dashboards.ListFor(r.Context(), acct)
		if err != nil {
			s.log.WarnContext(r.Context(), "dashboard: list custom dashboards", "err", err)
			data.Error = "Dashboards could not be loaded; try again shortly."
		}
		data.Dashboards = list
	}
	s.renderCustomDashboardPage(w, r, acct, "Dashboards", "custom_dashboards", data)
}

// renderCustomDashboard serves GET /dashboard/dashboards/{id}: every widget
// resolved into a card.
func (s *server) renderCustomDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data := dashboard.CustomDashboardData{Enabled: s.dashboards.Enabled}
	if data.Enabled {
		read, err := s.dashboards.Read(r.Context(), acct, r.PathValue("id"))
		switch {
		case errors.Is(err, state.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			s.log.WarnContext(r.Context(), "dashboard: read custom dashboard", "err", err)
			data.Error = "This dashboard could not be loaded; try again shortly."
		default:
			data = dashboard.NewCustomDashboardData(read)
		}
	}
	s.renderCustomDashboardPage(w, r, acct, "Dashboard", "custom_dashboard", data)
}

func (s *server) renderCustomDashboardPage(w http.ResponseWriter, r *http.Request, acct state.Account, title, body string, data any) {
	page := dashboard.Page{Title: title, Body: body, Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
