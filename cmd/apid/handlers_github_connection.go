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
	status.CSRFToken = s.issueGitHubInstallManageCSRF(w, acct.ID)
	writeJSON(w, http.StatusOK, status)
}

// issueGitHubInstallManageCSRF mints the named form envelope shared by the
// status, sync, and disconnect surfaces. Keeping the cookie write in one
// helper ensures the JSON API and server-rendered dashboard use the same
// action binding and expiry semantics.
func (s *server) issueGitHubInstallManageCSRF(w http.ResponseWriter, accountID string) string {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, githubInstallManageAction, accountID, githubInstallManageCSRFCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: githubInstallManageCSRFCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode,
		MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
	})
	return token
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
	previous, err := s.unbindGitHubAppForAccount(r.Context(), app.ID, acct.ID)
	if err != nil {
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

// unbindGitHubAppForAccount performs the account- and app-scoped part of a
// disconnect. The caller owns CSRF validation and audit emission; returning
// the previous binding lets both the JSON API and dashboard redirect record
// the same repository without rereading after the delete.
func (s *server) unbindGitHubAppForAccount(ctx context.Context, appID, accountID string) (state.GitHubBinding, error) {
	previous, previousErr := s.store.GetGithubInstallBindingForApp(ctx, appID, accountID)
	if previousErr != nil && !errors.Is(previousErr, state.ErrNotFound) {
		return state.GitHubBinding{}, api.ErrCapacity("could not read GitHub connection")
	}
	if err := s.githubd.UnbindAppRepo(ctx, appID, accountID); err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			return state.GitHubBinding{}, problem
		}
		return state.GitHubBinding{}, api.ErrCapacity("could not disconnect GitHub")
	}
	return previous, nil
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
	binding, result, err := s.syncGitHubAppForAccount(r.Context(), app.ID, acct.ID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not sync GitHub connection"))
		return
	}
	status, err := s.githubInstallStatus(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("GitHub sync completed but status could not be refreshed"))
		return
	}
	status.SyncResult = result
	acctID := acct.ID
	s.audit.Emit(r.Context(), "auth.install.synced", &acctID, map[string]any{
		"app_id":                  app.ID,
		"install_id":              binding.InstallID,
		"repo_full_name":          binding.RepoFullName,
		"remote_repository_count": result.RemoteRepositoryCount,
		"detached":                result.Detached,
	})
	writeJSON(w, http.StatusOK, status)
}

// syncGitHubAppForAccount performs the network reconciliation and durable
// health projection shared by the JSON API and server-rendered dashboard.
// It deliberately returns the pre-sync binding so callers can emit an audit
// row even when GitHub access was removed and the binding was detached.
func (s *server) syncGitHubAppForAccount(ctx context.Context, appID, accountID string) (state.GitHubBinding, *githubInstallSyncResult, error) {
	binding, err := s.store.GetGithubInstallBindingForApp(ctx, appID, accountID)
	if errors.Is(err, state.ErrNotFound) {
		return state.GitHubBinding{}, nil, api.NewProblem(http.StatusConflict, "github_not_bound",
			"GitHub is not connected to this app", "bind a repository before requesting a sync")
	}
	if err != nil {
		return state.GitHubBinding{}, nil, api.ErrCapacity("could not read GitHub connection")
	}
	if _, err := s.store.GitHubInstallForAccountInstallation(ctx, accountID, binding.InstallID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return binding, nil, api.NewProblem(http.StatusConflict, "github_install_not_found",
				"GitHub installation is no longer available", "reconnect GitHub before syncing this app")
		}
		return binding, nil, api.ErrCapacity("could not read GitHub installation")
	}
	repos, err := s.githubd.ListInstallableRepos(ctx, accountID, binding.InstallID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			return binding, nil, problem
		}
		return binding, nil, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute")
	}
	found := false
	for _, repo := range repos {
		if canonicalGitHubRepo(repo.FullName) == canonicalGitHubRepo(binding.RepoFullName) {
			found = true
			break
		}
	}
	if !found {
		if err := s.githubd.UnbindAppRepo(ctx, appID, accountID); err != nil {
			var problem *api.Problem
			if errors.As(err, &problem) {
				return binding, nil, problem
			}
			return binding, nil, api.ErrCapacity("could not detach inaccessible GitHub repository")
		}
	}
	now := time.Now().UTC()
	detached := 0
	if !found {
		detached = 1
	}
	if recorder, ok := s.store.(githubInstallationSyncRecorder); ok {
		if recordErr := recorder.RecordGitHubInstallationSync(ctx, binding.InstallID, now, "", len(repos), detached); recordErr != nil {
			return binding, nil, api.ErrCapacity("connection synced but health could not be recorded")
		}
	}
	return binding, &githubInstallSyncResult{Detached: !found, RemoteRepositoryCount: len(repos), SyncedAt: now}, nil
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
