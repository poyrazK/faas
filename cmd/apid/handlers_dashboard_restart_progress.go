package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppRestartProgressPath(rest string) (string, string, bool) {
	slug, rawID, ok := strings.Cut(strings.TrimSuffix(rest, "/"), "/restarts/")
	id, err := uuid.Parse(rawID)
	if !ok || !validSlug(slug) || err != nil || id == uuid.Nil {
		return "", "", false
	}
	return slug, id.String(), true
}

func (s *server) renderAppRestartProgress(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug, wakeID string) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AppOperationalReadTimeout)
	defer cancel()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID || app.Status == state.AppDeleted {
		http.NotFound(w, r)
		return
	}
	data := dashboard.AppRestartProgressData{App: dashboard.AppListItem{Slug: slug, Status: string(app.Status)}, WakeID: wakeID}
	status, problem := s.readRuntimeConfigRestartStatus(ctx, app.ID, wakeID)
	if problem != nil && problem.Status == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	if problem != nil {
		data.Unavailable = "Restart status could not be read. The accepted request may still be processing."
	} else {
		data.Status = &status
		if status.Status == "queued" || status.Status == "running" || status.Status == "retrying" {
			data.RefreshSeconds = int(api.AppRestartDashboardRefresh / time.Second)
		}
	}
	appCount, _ := s.store.CountDeployedApps(ctx, acct.ID)
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{Title: "Restart progress — " + slug, Body: "restart_progress", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}
