package apidserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

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
	if err != nil || !dashboardAppOwner(r, &acct, app) {
		http.NotFound(w, r)
		return
	}
	limits, planKnown := api.LimitsFor(acct.Plan)
	data := dashboard.AppLogDrainsData{
		App:         dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		PlanAllowed: planKnown && limits.LogDrainPerApp > 0,
	}
	// Drains are listed on every plan, so an owner whose plan stopped
	// including them sees the ones delivery paused (logs audit 2026-10-31 D3).
	data.Drains, data.ErrorMessage = s.dashboardLogDrainItems(r, log, acct, app)
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

// dashboardLogDrainItems renders app's drains with their health, plan
// coverage and last-24h analytics, and the message to show when part of that
// could not be read. Targets are masked as the API masks them.
func (s *server) dashboardLogDrainItems(r *http.Request, log *slog.Logger, acct state.Account, app state.App) ([]dashboard.LogDrainPageItem, string) {
	ctx := r.Context()
	drains, err := s.store.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil {
		log.Warn("dashboard log drains: list drains", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return nil, "Log-drain data is temporarily unavailable. Please try again shortly."
	}
	message := ""
	healthRows, err := s.store.ListAppLogDrainHealthForApp(ctx, app.ID)
	if err != nil {
		message = "Log-drain health is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard log drains: list health", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	healthByID := make(map[string]state.AppLogDrainHealth, len(healthRows))
	for _, health := range healthRows {
		healthByID[health.DrainID] = health
	}
	paused, err := s.store.ListAppLogDrainsPausedByPlan(ctx, app.ID)
	if err != nil {
		message = "Log-drain health is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard log drains: list plan coverage", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	reveal := appLogDrainTargetVisible(r)
	now := time.Now().UTC()
	items := make([]dashboard.LogDrainPageItem, 0, len(drains))
	for _, drain := range drains {
		item := dashboardLogDrainPageItem(drain, healthByID[drain.ID])
		if drain.Enabled && slices.Contains(paused, drain.ID) {
			item.PlanPaused, item.Status, item.Active = true, api.AppLogDrainHealthStatusInactive, false
		}
		if !reveal {
			item.TargetURL = api.MaskAppLogDrainTargetURL(item.TargetURL)
		}
		s.addDashboardLogDrainAnalytics(ctx, log, acct, &item, now)
		items = append(items, item)
	}
	return items, message
}

// addDashboardLogDrainAnalytics fills item's last-24h delivery summary.
func (s *server) addDashboardLogDrainAnalytics(ctx context.Context, log *slog.Logger, acct state.Account, item *dashboard.LogDrainPageItem, now time.Time) {
	from := now.Add(-24 * time.Hour)
	firstBucket := from.Truncate(appLogDrainAnalyticsBucketInterval).Add(appLogDrainAnalyticsBucketInterval)
	samples, err := s.store.ListAppLogDrainDeliveryAnalytics(ctx, item.ID,
		firstBucket.Add(-appLogDrainAnalyticsBucketInterval), now.Truncate(appLogDrainAnalyticsBucketInterval))
	if err != nil {
		log.Warn("dashboard log drains: list analytics", "account_id", acct.ID, "drain_id", item.ID, "err", err)
		return
	}
	analytics := appLogDrainAnalyticsResponse(samples, item.ID, "24h", from, now)
	if len(analytics.Buckets) == 0 {
		return
	}
	item.AnalyticsAvailable = true
	item.AnalyticsDelivered = analytics.Summary.Delivered
	item.AnalyticsFailed = analytics.Summary.Failed
	item.AnalyticsDropped = analytics.Summary.Dropped
	item.AnalyticsRetries = analytics.Summary.Retries
	item.AnalyticsSuccessRate = fmt.Sprintf("%.2f%%", analytics.Summary.SuccessRate*100)
	item.AnalyticsAverageLatency = fmt.Sprintf("%.1f ms", analytics.Summary.AverageLatencyMS)
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
