// Developer environments dashboard — the browser companion to `gregale dev`.
// It projects the existing developer-app rows instead of adding a new
// persistence path, so the CLI and dashboard cannot disagree about ownership
// or lease cleanup.
package main

import (
	"errors"
	"log/slog"
	"net/http"

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
