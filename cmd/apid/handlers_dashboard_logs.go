package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderAppLogs renders /dashboard/apps/{slug}/logs. Log lines are streamed
// from gatewayd-internal by the browser; apid only projects the bounded
// deployment and instance selectors needed by the page.
func (s *server) renderAppLogs(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	deployments, err := s.store.ListDeploymentsForApp(ctx, app.ID, 50, 0)
	if err != nil {
		log.Warn("dashboard renderAppLogs: list deployments", "account_id", acct.ID, "app_id", app.ID, "err", err)
		deployments = nil
	}
	instances, err := s.store.ListLatestInstancesForApp(ctx, app.ID, 25)
	if err != nil {
		log.Warn("dashboard renderAppLogs: list instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		instances = nil
	}

	depItems := make([]dashboard.LogDeploymentItem, 0, len(deployments))
	for _, dep := range deployments {
		depItems = append(depItems, dashboard.LogDeploymentItem{
			ID:        dep.ID,
			Status:    string(dep.Status),
			CreatedAt: dep.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	instanceItems := make([]dashboard.LogInstanceItem, 0, len(instances))
	for _, instance := range instances {
		instanceItems = append(instanceItems, dashboard.LogInstanceItem{
			ID:        instance.ID,
			State:     instance.State,
			StartedAt: instance.StartedAt.UTC().Format(time.RFC3339),
		})
	}

	archiveEnabled := acct.Plan.LogArchiveEnabled()
	archiveDate := time.Now().UTC().Format("2006-01-02")
	archiveURL := ""
	archiveInstanceID := ""
	if archiveEnabled && len(instanceItems) > 0 {
		archiveInstanceID = instanceItems[0].ID
		archiveURL = dashboardArchiveLogsURL(slug, archiveInstanceID, archiveDate)
	}

	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppLogs: count deployed apps", "account_id", acct.ID, "err", err)
	}
	page := dashboard.Page{
		Title:   app.Slug + " logs",
		Body:    "logs",
		Account: dashboardAccountView(acct, appCount),
		Data: dashboard.LogsData{
			AppSlug:           app.Slug,
			AppStatus:         string(app.Status),
			StreamURL:         dashboardAppLogsURL(app.Slug, q),
			ArchiveBase:       dashboardArchiveLogsBase(app.Slug),
			ArchiveURL:        archiveURL,
			ArchiveDate:       archiveDate,
			ArchiveInstanceID: archiveInstanceID,
			ArchiveEnabled:    archiveEnabled,
			Level:             q.Get("level"),
			Grep:              q.Get("grep"),
			Since:             q.Get("since"),
			Deployment:        q.Get("deployment"),
			Deployments:       depItems,
			Instances:         instanceItems,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardAppLogsURL(slug string, query url.Values) string {
	values := url.Values{}
	values.Set("follow", "1")
	for _, key := range []string{"level", "grep", "since", "deployment"} {
		if value := query.Get(key); value != "" {
			values.Set(key, value)
		}
	}
	return dashboardArchiveLogsBase(slug) + "?" + values.Encode()
}

func dashboardArchiveLogsBase(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/logs"
}

func dashboardArchiveLogsURL(slug, instance, day string) string {
	values := url.Values{}
	values.Set("archive", "1")
	values.Set("instance", instance)
	values.Set("date", day)
	return dashboardArchiveLogsBase(slug) + "?" + values.Encode()
}

// parseAppLogsPath splits a /dashboard/apps/{slug}/logs suffix. The trailing
// slash is accepted because the dashboard's canonical app links occasionally
// preserve one when copied from a browser.
func parseAppLogsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/logs"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}
