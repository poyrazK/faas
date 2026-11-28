// adr: 230 — the dashboard's read-only history of an app's tasks.
package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apptaskdashboard"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// renderDashboardAppTasks serves GET /dashboard/apps/{slug}/tasks. The page
// is assembled by apptaskdashboard; tasks are started and cancelled from the
// CLI or MCP, so the page never writes.
func (s *server) renderDashboardAppTasks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AppOperationalReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return
	}
	data := apptaskdashboard.Build(ctx, s.store, apptaskdashboard.Request{
		AccountID: acct.ID, AppID: app.ID, Slug: app.Slug, Enabled: s.appTaskAPIEnabled,
		Query: r.URL.Query(), Response: appTaskResponse,
	})
	count, _ := s.store.CountDeployedApps(ctx, acct.ID)
	page := dashboard.Page{Title: "Tasks — " + app.Slug, Body: "app_tasks", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
