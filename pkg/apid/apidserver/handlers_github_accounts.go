package apidserver

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/githubconnect"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

// These browser routes manage deployment authorization, never login identity.
func (s *server) manageGitHubAccountConnections(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(401, api.CodeUnauthorized, "Unauthorized", "Sign in to manage GitHub."))
		return
	}
	store, ok := s.store.(githubconnect.AccountStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("GitHub connection management is unavailable."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		connections, err := githubconnect.List(r.Context(), store, acct.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("Could not load GitHub connections."))
			return
		}
		token := s.issueGitHubManagementCSRF(w, acct.ID, "github_account_manage", "faas_csrf_github_account")
		if token == "" {
			api.WriteProblem(w, api.ErrCapacity("Could not prepare GitHub connection management."))
			return
		}
		writeJSON(w, 200, struct {
			Connections []githubconnect.Connection `json:"connections"`
			CSRFToken   string                     `json:"csrf_token"`
		}{connections, token})
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, "github_account_manage", acct.ID, "faas_csrf_github_account"); err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid CSRF token", "Reload the page and try again."))
		return
	}
	installID, err := strconv.ParseInt(r.PathValue("installationID"), 10, 64)
	if err != nil || installID <= 0 {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid installation", "Choose a connected installation."))
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.DisconnectGitHubAccountInstallation(r.Context(), acct.ID, installID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("Could not disconnect GitHub."))
			return
		}
		accountID := acct.ID
		s.audit.Emit(r.Context(), "auth.install.disconnected", &accountID, map[string]any{"install_id": installID})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	client, ok := s.githubd.(githubconnect.AccessClient)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("GitHub verification is unavailable. Try again after the service is updated."))
		return
	}
	verified, err := githubconnect.Verify(r.Context(), store, client, acct.ID, installID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
		} else if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(404, "github_install_not_found", "GitHub connection not found", "Reconnect GitHub in Settings."))
		} else {
			api.WriteProblem(w, api.ErrCapacity("Could not verify GitHub access. GitHub may be unavailable; try again shortly."))
		}
		return
	}
	writeJSON(w, 200, verified)
}
