package main

// Customer-facing GitHub connection management. The installation drift
// worker keeps durable bindings safe in the background; these handlers make
// that state visible and let an account repair a single app immediately.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	githubInstallManageAction     = "github_install_manage"
	githubInstallManageCSRFCookie = "faas_csrf_github_install"
)

// githubInstallStatusResponse intentionally excludes sealed credentials. It
// is safe to return to the dashboard and gives customers enough information
// to understand whether pushes can currently reach an app.
type githubInstallStatusResponse struct {
	State                        string                   `json:"state"`
	Health                       string                   `json:"health"`
	Connected                    bool                     `json:"connected"`
	InstallationID               int64                    `json:"installation_id,omitempty"`
	GitHubLogin                  string                   `json:"github_login,omitempty"`
	DefaultBranch                string                   `json:"default_branch,omitempty"`
	RepoFullName                 string                   `json:"repo_full_name,omitempty"`
	ProductionBranch             string                   `json:"production_branch,omitempty"`
	BindingID                    string                   `json:"binding_id,omitempty"`
	LinkedAt                     *time.Time               `json:"linked_at,omitempty"`
	LastReconciledAt             *time.Time               `json:"last_reconciled_at,omitempty"`
	LastReconcileError           string                   `json:"last_reconcile_error,omitempty"`
	LastReconcileRepositoryCount int                      `json:"last_reconcile_repository_count"`
	LastReconcileDetachedCount   int                      `json:"last_reconcile_detached_count"`
	CSRFToken                    string                   `json:"csrf_token,omitempty"`
	SyncResult                   *githubInstallSyncResult `json:"sync_result,omitempty"`
}

type githubInstallSyncResult struct {
	Detached              bool      `json:"detached"`
	RemoteRepositoryCount int       `json:"remote_repository_count"`
	SyncedAt              time.Time `json:"synced_at"`
}

// getGitHubInstallStatus returns the account-scoped installation and app
// binding state. An uninstalled or unbound app is a successful read with
// connected=false, which keeps the dashboard from treating a normal setup
// step as an error.
func (s *server) getGitHubInstallStatus(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to inspect the GitHub connection"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	status, err := s.githubInstallStatus(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read GitHub connection status"))
		return
	}
	if token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, githubInstallManageAction, acct.ID, githubInstallManageCSRFCookie); tokenErr == nil {
		status.CSRFToken = token
		http.SetCookie(w, &http.Cookie{
			Name: githubInstallManageCSRFCookie, Value: token, Path: "/", HttpOnly: true,
			Secure: s.domain != "", SameSite: http.SameSiteLaxMode,
			MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
		})
	}
	writeJSON(w, http.StatusOK, status)
}

// unbindGitHubApp removes one app's binding. The app lookup is account
// scoped, and githubd owns cache invalidation so a disconnected app stops
// receiving pushes on every githubd instance that services the request.
func (s *server) unbindGitHubApp(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to disconnect GitHub"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubInstallManageAction, acct.ID, githubInstallManageCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	previous, previousErr := s.store.GetGithubInstallBindingForApp(r.Context(), app.ID, acct.ID)
	if previousErr != nil && !errors.Is(previousErr, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read GitHub connection"))
		return
	}
	if err := s.githubd.UnbindAppRepo(r.Context(), app.ID, acct.ID); err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not disconnect GitHub"))
		return
	}
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.unbound", &acctID, map[string]any{
		"app_id":         app.ID,
		"repo_full_name": previous.RepoFullName,
		"binding_id":     previous.BindingID,
	})
	status, err := s.githubInstallStatus(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("GitHub disconnected but status could not be refreshed"))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// syncGitHubApp performs an app-scoped, on-demand reconciliation. It asks
// GitHub for the installation's current repository list and detaches this
// app only when its repository is no longer accessible. The periodic worker
// remains responsible for account-wide reconciliation; this endpoint gives
// the customer an immediate answer after changing GitHub permissions.
func (s *server) syncGitHubApp(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "sign in to sync the GitHub connection"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, githubInstallManageAction, acct.ID, githubInstallManageCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	binding, err := s.store.GetGithubInstallBindingForApp(r.Context(), app.ID, acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "github_not_bound",
			"GitHub is not connected to this app", "bind a repository before requesting a sync"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read GitHub connection"))
		return
	}
	if _, err := s.store.GitHubInstallForAccountInstallation(r.Context(), acct.ID, binding.InstallID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "github_install_not_found",
				"GitHub installation is no longer available", " reconnect GitHub before syncing this app"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read GitHub installation"))
		return
	}
	repos, err := s.githubd.ListInstallableRepos(r.Context(), acct.ID, binding.InstallID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute"))
		return
	}
	found := false
	for _, repo := range repos {
		if canonicalGitHubRepo(repo.FullName) == canonicalGitHubRepo(binding.RepoFullName) {
			found = true
			break
		}
	}
	if !found {
		if err := s.githubd.UnbindAppRepo(r.Context(), app.ID, acct.ID); err != nil {
			var problem *api.Problem
			if errors.As(err, &problem) {
				api.WriteProblem(w, problem)
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not detach inaccessible GitHub repository"))
			return
		}
	}
	now := time.Now().UTC()
	if recorder, ok := s.store.(githubInstallationSyncRecorder); ok {
		detached := 0
		if !found {
			detached = 1
		}
		if recordErr := recorder.RecordGitHubInstallationSync(r.Context(), binding.InstallID, now, "", len(repos), detached); recordErr != nil {
			api.WriteProblem(w, api.ErrCapacity("connection synced but health could not be recorded"))
			return
		}
	}
	status, err := s.githubInstallStatus(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("GitHub sync completed but status could not be refreshed"))
		return
	}
	status.SyncResult = &githubInstallSyncResult{Detached: !found, RemoteRepositoryCount: len(repos), SyncedAt: now}
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.synced", &acctID, map[string]any{
		"app_id":                  app.ID,
		"install_id":              binding.InstallID,
		"repo_full_name":          binding.RepoFullName,
		"remote_repository_count": len(repos),
		"detached":                !found,
	})
	writeJSON(w, http.StatusOK, status)
}

type githubInstallationSyncRecorder interface {
	RecordGitHubInstallationSync(context.Context, int64, time.Time, string, int, int) error
}

func (s *server) githubInstallStatus(ctx context.Context, accountID, appID string) (githubInstallStatusResponse, error) {
	status := githubInstallStatusResponse{State: "not_installed", Health: "not_connected"}
	install, installErr := s.store.GitHubInstallForAccount(ctx, accountID)
	if installErr != nil && !errors.Is(installErr, state.ErrNotFound) {
		return status, installErr
	}
	if installErr == nil {
		status.State = "installed"
		status.InstallationID = install.InstallationID
		status.GitHubLogin = install.AuditGithubLogin
		status.DefaultBranch = install.DefaultBranch
		status.LastReconciledAt = install.LastReconciledAt
		status.LastReconcileError = install.LastReconcileError
		status.LastReconcileRepositoryCount = install.LastReconcileRepositoryCount
		status.LastReconcileDetachedCount = install.LastReconcileDetachedCount
		status.Health = "unknown"
		if install.LastReconcileError != "" {
			status.Health = "degraded"
		} else if install.LastReconciledAt != nil {
			status.Health = "healthy"
		}
	}
	binding, bindingErr := s.store.GetGithubInstallBindingForApp(ctx, appID, accountID)
	if bindingErr != nil && !errors.Is(bindingErr, state.ErrNotFound) {
		return status, bindingErr
	}
	if bindingErr == nil {
		status.State = "bound"
		status.Connected = true
		status.InstallationID = binding.InstallID
		status.RepoFullName = binding.RepoFullName
		status.ProductionBranch = binding.ProductionBranch
		status.BindingID = binding.BindingID
		if !binding.LinkedAt.IsZero() {
			linkedAt := binding.LinkedAt
			status.LinkedAt = &linkedAt
		}
		if installErr != nil {
			status.Health = "degraded"
		}
	}
	return status, nil
}

func canonicalGitHubRepo(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
