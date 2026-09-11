package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// parseAppLogDrainsPath recognizes the customer-facing per-app log-drain
// health page.
func parseAppLogDrainsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/log-drains"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderAppLogDrains(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	limits, planKnown := api.LimitsFor(acct.Plan)
	data := dashboard.AppLogDrainsData{
		App:         dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		PlanAllowed: planKnown && limits.LogDrainPerApp > 0,
	}
	if data.PlanAllowed {
		drains, listErr := s.store.ListAppLogDrainsForApp(ctx, app.ID)
		if listErr != nil {
			data.ErrorMessage = "Log-drain data is temporarily unavailable. Please try again shortly."
			log.Warn("dashboard log drains: list drains", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
		} else {
			healthRows, healthErr := s.store.ListAppLogDrainHealthForApp(ctx, app.ID)
			if healthErr != nil {
				data.ErrorMessage = "Log-drain health is temporarily unavailable. Please try again shortly."
				log.Warn("dashboard log drains: list health", "account_id", acct.ID, "app_id", app.ID, "err", healthErr)
			}
			healthByID := make(map[string]state.AppLogDrainHealth, len(healthRows))
			for _, health := range healthRows {
				healthByID[health.DrainID] = health
			}
			data.Drains = make([]dashboard.LogDrainPageItem, 0, len(drains))
			for _, drain := range drains {
				data.Drains = append(data.Drains, dashboardLogDrainPageItem(drain, healthByID[drain.ID]))
			}
		}
	}
	view, _ := AccountFrom(ctx)
	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard log drains: count deployed apps", "account_id", acct.ID, "err", countErr)
	}
	page := dashboard.Page{
		Title:   "Log drains — " + app.Slug,
		Body:    "log_drains",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardLogDrainPageItem(drain state.AppLogDrain, health state.AppLogDrainHealth) dashboard.LogDrainPageItem {
	status := api.NormalizeAppLogDrainHealthStatus(health.Status)
	active := health.Active
	if !drain.Enabled {
		status = api.AppLogDrainHealthStatusInactive
		active = false
	}
	return dashboard.LogDrainPageItem{
		ID:                    drain.ID,
		Kind:                  string(drain.Kind),
		TargetURL:             drain.TargetURL,
		Enabled:               drain.Enabled,
		Status:                status,
		Active:                active,
		QueueDepth:            health.QueueDepth,
		QueueCapacity:         health.QueueCapacity,
		PendingRecords:        health.PendingRecords,
		PendingBytes:          health.PendingBytes,
		PendingBytesCapacity:  health.PendingBytesCapacity,
		DeadLetterTotal:       health.DeadLetterTotal,
		OldestPendingAt:       api.FormatAlertTime(health.OldestPendingAt),
		DeliveredTotal:        health.DeliveredTotal,
		FailedTotal:           health.FailedTotal,
		DroppedTotal:          health.DroppedTotal,
		RetriesTotal:          health.RetriesTotal,
		StreamReconnectsTotal: health.StreamReconnectsTotal,
		GapsTotal:             health.GapsTotal,
		LastSuccessAt:         api.FormatAlertTime(health.LastSuccessAt),
		LastFailureAt:         api.FormatAlertTime(health.LastFailureAt),
		LastError:             api.SanitizeAppLogDrainHealthError(health.LastError),
		UpdatedAt:             api.FormatAlertTime(health.UpdatedAt),
	}
}
