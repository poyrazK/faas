package main

// Server-rendered customer controls for an app's GitHub connection. The
// underlying JSON endpoints remain the canonical API; these handlers add the
// CSRF-bound form + redirect experience used by the dashboard.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func githubDashboardFlash(r *http.Request) string {
	switch r.URL.Query().Get("github") {
	case "synced":
		return "GitHub access synced."
	case "detached":
		return "GitHub access was removed, so this app was detached from its repository."
	case "disconnected":
		return "GitHub repository disconnected from this app."
	case "sync-error":
		return "GitHub access could not be synced. Try again in a minute."
	case "disconnect-error":
		return "GitHub could not be disconnected. Try again."
	case "bind-error":
		return "The app was created, but the GitHub repository could not be connected. Retry from this page or reconnect GitHub."
	case "forbidden":
		return "The GitHub action expired. Reload the page and try again."
	default:
		return ""
	}
}

func (s *server) dashboardGitHubConnection(ctx context.Context, log *slog.Logger, w http.ResponseWriter, acct state.Account, app state.App, flash string) *dashboard.GitHubConnectionView {
	status, err := s.githubInstallStatus(ctx, acct.ID, app.ID)
	if err != nil {
		log.Warn("dashboard GitHub connection: read status", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return &dashboard.GitHubConnectionView{Available: false, Flash: flash}
	}
	return &dashboard.GitHubConnectionView{
		Available:                    true,
		State:                        status.State,
		Health:                       status.Health,
		Connected:                    status.Connected,
		GitHubLogin:                  status.GitHubLogin,
		RepoFullName:                 status.RepoFullName,
		ProductionBranch:             status.ProductionBranch,
		LastReconciledAt:             optionalGitHubTime(status.LastReconciledAt),
		LastReconcileError:           status.LastReconcileError,
		LastReconcileRepositoryCount: status.LastReconcileRepositoryCount,
		LastReconcileDetachedCount:   status.LastReconcileDetachedCount,
		Activity:                     projectDashboardGitHubActivity(status.Activity, status.RepoFullName, app.Slug),
		CSRFToken:                    s.issueGitHubInstallManageCSRF(w, acct.ID),
		Flash:                        flash,
	}
}

func projectDashboardGitHubActivity(activity *githubActivityResponse, repoFullName, appSlug string) *dashboard.GitHubActivityView {
	if activity == nil {
		return nil
	}
	out := &dashboard.GitHubActivityView{
		WebhookDeliveries: make([]dashboard.GitHubWebhookActivityView, 0, len(activity.WebhookDeliveries)),
		CheckUpdates:      make([]dashboard.GitHubCheckActivityView, 0, len(activity.CheckUpdates)),
	}
	for _, item := range activity.WebhookDeliveries {
		status, class, guidance := dashboardGitHubWebhookStatus(item.Status)
		_, commitURL, _, _, commitShort := githubDeploymentLinks(
			"github://"+repoFullName+"@"+item.CommitSHA, item.CommitSHA)
		out.WebhookDeliveries = append(out.WebhookDeliveries, dashboard.GitHubWebhookActivityView{
			EventType: item.EventType, Status: status, StatusClass: class, CommitShort: commitShort,
			CommitURL: commitURL, ReceivedAt: item.ReceivedAt.UTC().Format(time.RFC3339), Guidance: guidance,
		})
	}
	for _, item := range activity.CheckUpdates {
		status, class, guidance := dashboardGitHubCheckStatus(item.Status)
		_, _, _, _, commitShort := githubDeploymentLinks(
			"github://"+repoFullName+"@"+item.CommitSHA, item.CommitSHA)
		deploymentURL := ""
		if item.DeploymentID != "" {
			deploymentURL = "/dashboard/apps/" + url.PathEscape(appSlug) + "/deployments/" + url.PathEscape(item.DeploymentID)
		}
		out.CheckUpdates = append(out.CheckUpdates, dashboard.GitHubCheckActivityView{
			DeploymentID: item.DeploymentID, DeploymentURL: deploymentURL, Status: status, StatusClass: class,
			CommitShort: commitShort, UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339), Guidance: guidance,
		})
	}
	return out
}

func dashboardGitHubWebhookStatus(status string) (label, class, guidance string) {
	switch status {
	case "succeeded":
		return "processed", "running", ""
	case "pending":
		return "queued", "waking", "Gregale received the event and is waiting to process it."
	case "processing":
		return "processing", "waking", "Gregale received the event and is processing it."
	case "dead":
		return "needs attention", "cert-failed", "Gregale received the event but could not process it. Sync GitHub access and try another push."
	default:
		return "unknown", "dim", "Refresh the page or sync GitHub access if this persists."
	}
}

func dashboardGitHubCheckStatus(status string) (label, class, guidance string) {
	switch status {
	case "succeeded":
		return "synced", "running", ""
	case "pending":
		return "queued", "waking", "Gregale is waiting to publish the deployment status to GitHub."
	case "processing":
		return "syncing", "waking", "Gregale is publishing the deployment status to GitHub."
	case "dead":
		return "not synced", "cert-failed", "The deployment is still available in Gregale, but its GitHub Check Run needs attention. Sync GitHub access if this repeats."
	default:
		return "unknown", "dim", "Refresh the page or sync GitHub access if this persists."
	}
}

func optionalGitHubTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func dashboardGitHubRedirect(w http.ResponseWriter, r *http.Request, slug, flash string) {
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	target := "/dashboard/apps/" + url.PathEscape(slug)
	if flash != "" {
		target += "?github=" + url.QueryEscape(flash)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *server) dashboardGitHubSync(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubInstallManageAction, acct.ID, githubInstallManageCSRFCookie); err != nil {
		dashboardGitHubRedirect(w, r, slug, "forbidden")
		return
	}
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	binding, result, err := s.syncGitHubAppForAccount(r.Context(), app.ID, acct.ID)
	if err != nil {
		var problem *api.Problem
		if !errors.As(err, &problem) {
			s.log.Warn("dashboard GitHub connection: sync", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
		dashboardGitHubRedirect(w, r, slug, "sync-error")
		return
	}
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.synced", &acctID, map[string]any{
		"app_id":                  app.ID,
		"install_id":              binding.InstallID,
		"repo_full_name":          binding.RepoFullName,
		"remote_repository_count": result.RemoteRepositoryCount,
		"detached":                result.Detached,
		"surface":                 "dashboard",
	})
	if result.Detached {
		dashboardGitHubRedirect(w, r, slug, "detached")
		return
	}
	dashboardGitHubRedirect(w, r, slug, "synced")
}

func (s *server) dashboardGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubInstallManageAction, acct.ID, githubInstallManageCSRFCookie); err != nil {
		dashboardGitHubRedirect(w, r, slug, "forbidden")
		return
	}
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	previous, err := s.unbindGitHubAppForAccount(r.Context(), app.ID, acct.ID)
	if err != nil {
		s.log.Warn("dashboard GitHub connection: disconnect", "account_id", acct.ID, "app_id", app.ID, "err", err)
		dashboardGitHubRedirect(w, r, slug, "disconnect-error")
		return
	}
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.unbound", &acctID, map[string]any{
		"app_id":         app.ID,
		"repo_full_name": previous.RepoFullName,
		"binding_id":     previous.BindingID,
		"surface":        "dashboard",
	})
	dashboardGitHubRedirect(w, r, slug, "disconnected")
}
