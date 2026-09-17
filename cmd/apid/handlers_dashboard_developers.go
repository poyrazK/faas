// Developer environments dashboard — the browser companion to `gregale dev`.
// It projects the existing developer-app rows instead of adding a new
// persistence path, so the CLI and dashboard cannot disagree about ownership
// or lease cleanup.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderDeveloperEnvironments renders /dashboard/developers. Developer apps
// are filtered from the account-scoped preview query with the same predicate
// used by the CLI and quota paths; PR previews and production apps never land
// on this page.
func (s *server) renderDeveloperEnvironments(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	rows, err := s.store.ListPreviewsForAccount(ctx, acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}

	latestInstances, err := s.store.ListLatestInstancePerApp(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderDeveloperEnvironments: latest instance per app", "account_id", acct.ID, "err", err)
		latestInstances = nil
	}

	items := make([]dashboard.DeveloperEnvironmentItem, 0, len(rows))
	historyStore, hasHistory := s.store.(state.DevSyncHistoryStore)
	for _, app := range rows {
		if !state.IsDeveloperApp(app) {
			continue
		}

		item := dashboard.DeveloperEnvironmentItem{
			Slug:           app.Slug,
			ParentSlug:     app.PreviewOfSlug,
			Runtime:        app.Runtime,
			Status:         string(app.Status),
			URL:            appURLForDomain(app.Slug, s.domain),
			ExpiresAt:      app.PreviewExpiresAt,
			DetailsURL:     "/dashboard/apps/" + app.Slug,
			LogsURL:        "/dashboard/apps/" + app.Slug + "/logs",
			AnalyticsURL:   "/dashboard/apps/" + app.Slug + "#analytics",
			EnvironmentURL: "/dashboard/apps/" + app.Slug + "/env",
		}
		item.StateBadge, item.StateBadgeGlyph, item.StateBadgeLabel = dashboard.BadgeForDefault()
		if instance, ok := latestInstances[app.ID]; ok {
			item.StateBadge, item.StateBadgeGlyph, item.StateBadgeLabel = dashboard.BadgeFor(state.State(instance.State))
		}

		if deployment, deployErr := s.store.LatestDeployment(ctx, app.ID); deployErr == nil {
			item.HasDeployment = true
			item.LatestDeployment = dashboardDeploymentItem(deployment)
		} else if !errors.Is(deployErr, state.ErrNotFound) {
			log.Warn("dashboard renderDeveloperEnvironments: latest deployment", "account_id", acct.ID, "app_id", app.ID, "err", deployErr)
		}
		if hasHistory && historyStore != nil {
			if history, historyErr := historyStore.ListDevSyncHistory(ctx, app.ID, 20); historyErr == nil {
				item.SyncSummary = developerSyncDashboardSummary(history)
				item.SyncHistory = make([]dashboard.DeveloperSyncHistoryItem, 0, len(history))
				for _, sync := range history {
					item.SyncHistory = append(item.SyncHistory, dashboard.DeveloperSyncHistoryItem{
						Status: sync.Status, EditToLive: formatDashboardDevDuration(sync.EditToLiveMS),
						CreatedAt: sync.CreatedAt.Local().Format("2006-01-02 15:04"), WithinSLO: sync.WithinSLO,
					})
				}
			} else if !errors.Is(historyErr, state.ErrNotFound) {
				log.Warn("dashboard renderDeveloperEnvironments: sync history", "account_id", acct.ID, "app_id", app.ID, "err", historyErr)
			}
		}
		items = append(items, item)
	}

	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderDeveloperEnvironments: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	limits := api.MustLimitsFor(acct.Plan)
	page := dashboard.Page{
		Title:   "Developer environments",
		Body:    "developer_environments",
		Account: dashboardAccountView(view, appCount),
		Data: dashboard.DeveloperEnvironmentsData{
			Environments: items,
			Used:         len(items),
			Limit:        limits.DeveloperApps,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func formatDashboardDevDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}

func developerSyncDashboardSummary(rows []state.DevSyncHistory) dashboard.DeveloperSyncSummary {
	summary := summarizeDevSyncHistory(rows)
	return dashboard.DeveloperSyncSummary{
		Count: summary.Count, WithinSLOCount: summary.WithinSLOCount,
		P50:       formatDashboardDevDuration(summary.P50EditToLiveMS),
		P95:       formatDashboardDevDuration(summary.P95EditToLiveMS),
		SLOTarget: formatDashboardDevDuration(summary.SLOTargetMS), Guidance: summary.Guidance,
	}
}
