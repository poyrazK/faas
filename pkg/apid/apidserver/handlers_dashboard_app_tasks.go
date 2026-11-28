// adr: 230 — the dashboard's read-only history of an app's tasks.
package apidserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

const dashboardAppTasksPageSize = 25

// renderDashboardAppTasks serves GET /dashboard/apps/{slug}/tasks: newest
// first, ?offset= pages, ?task= opens one task with its output tails. Tasks
// are started and cancelled from the CLI or MCP, so the page never writes.
func (s *server) renderDashboardAppTasks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AppOperationalReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return
	}
	data := dashboard.AppTasksData{Slug: app.Slug, Enabled: s.appTaskAPIEnabled, Limit: dashboardAppTasksPageSize}
	if data.Enabled {
		if id := r.URL.Query().Get("task"); id != "" {
			if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
				data.Message = "That task ID is not valid."
			} else if task, err := s.store.AppTaskByID(ctx, acct.ID, app.ID, id); err != nil {
				data.Message = "That task was not found for this app."
			} else {
				row := dashboard.NewAppTaskRow(appTaskResponse(task))
				data.Selected = &row
			}
		}
		if data.Selected == nil {
			if raw := r.URL.Query().Get("offset"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
					data.Offset = parsed
				}
			}
			// One extra row says whether an older page exists.
			rows, err := s.store.ListAppTasks(ctx, acct.ID, app.ID, data.Limit+1, data.Offset)
			if err != nil {
				data.Message = "Task history is unavailable right now; try again shortly."
			} else {
				if len(rows) > data.Limit {
					rows, data.HasNext, data.NextOffset = rows[:data.Limit], true, data.Offset+data.Limit
				}
				for _, task := range rows {
					data.Tasks = append(data.Tasks, dashboard.NewAppTaskRow(appTaskResponse(task)))
				}
				if data.Offset > 0 {
					data.HasPrev, data.PrevOffset = true, max(0, data.Offset-data.Limit)
				}
			}
		}
	}
	count, _ := s.store.CountDeployedApps(ctx, acct.ID)
	page := dashboard.Page{Title: "Tasks — " + app.Slug, Body: "app_tasks", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
