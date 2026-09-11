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
		CSRFToken:                    s.issueGitHubInstallManageCSRF(w, acct.ID),
		Flash:                        flash,
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
