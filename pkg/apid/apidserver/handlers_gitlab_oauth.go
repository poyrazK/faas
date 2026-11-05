package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	gitLabOAuthCallbackPath    = "/v1/gitlab/callback"
	gitLabOAuthStateCookie     = "faas_gitlab_oauth_state"
	gitLabConnectAction        = "connect_gitlab"
	gitLabConnectCSRFCookie    = "faas_csrf_gitlab_connect"
	gitLabDisconnectAction     = "disconnect_gitlab"
	gitLabDisconnectCSRFCookie = "faas_csrf_gitlab_disconnect"
)

func gitLabCookieSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *server) startGitLabOAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in to connect GitLab."))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, gitLabConnectAction, acct.ID, gitLabConnectCSRFCookie); err != nil {
		http.Redirect(w, r, "/dashboard/account?gitlab=connect-forbidden", http.StatusSeeOther)
		return
	}
	config := gitLabOAuthConfig()
	redirect, redirectErr := url.Parse(config.RedirectURI)
	if !config.Valid() || redirectErr != nil || redirect.Path != gitLabOAuthCallbackPath || redirect.RawQuery != "" ||
		hostHMACKey == nil || len(hostHMACKeyBytes()) == 0 || !gitLabSealingReady(r.Context()) {
		api.WriteProblem(w, api.ErrCapacity("GitLab OAuth is not configured."))
		return
	}
	authorize, value, err := gitlabsource.MintOAuthState(config, acct.ID, hostHMACKeyBytes(), time.Now())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not start GitLab OAuth."))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: gitLabOAuthStateCookie, Value: value, Path: gitLabOAuthCallbackPath,
		HttpOnly: true, Secure: gitLabCookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(gitlabsource.OAuthStateLifetime.Seconds())})
	http.Redirect(w, r, authorize, http.StatusSeeOther)
}

func (s *server) finishGitLabOAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in to connect GitLab."))
		return
	}
	cookie, err := r.Cookie(gitLabOAuthStateCookie)
	if err != nil || hostHMACKey == nil || len(hostHMACKeyBytes()) == 0 {
		api.WriteProblem(w, api.ErrValidation("GitLab OAuth state is missing."))
		return
	}
	verifier, valid := gitlabsource.VerifyOAuthState(cookie.Value, r.URL.Query().Get("state"), acct.ID, hostHMACKeyBytes(), time.Now())
	if !valid {
		api.WriteProblem(w, api.ErrValidation("GitLab OAuth state does not match this account."))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: gitLabOAuthStateCookie, Path: gitLabOAuthCallbackPath,
		HttpOnly: true, Secure: gitLabCookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	code := r.URL.Query().Get("code")
	config := gitLabOAuthConfig()
	tokens, err := config.Exchange(r.Context(), code, verifier)
	if err != nil {
		api.WriteProblem(w, api.ErrSourceRefUnavailable("GitLab OAuth code exchange failed."))
		return
	}
	user, err := s.gitLabClient().CurrentUser(r.Context(), tokens.Access)
	if err != nil {
		api.WriteProblem(w, api.ErrSourceRefUnavailable("GitLab OAuth identity could not be verified."))
		return
	}
	if err := s.saveGitLabOAuthConnection(r, acct.ID, user, tokens); err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not save GitLab connection."))
		return
	}
	s.audit.Emit(r.Context(), "gitlab.oauth.connected", &acct.ID, map[string]any{"gitlab_user_id": user.ID})
	http.Redirect(w, r, "/dashboard/account?gitlab=connected", http.StatusSeeOther)
}

func (s *server) saveGitLabOAuthConnection(r *http.Request, accountID string, user gitlabsource.User, tokens gitlabsource.Tokens) error {
	sealed, err := sealGitLabTokens(tokens)
	if err != nil {
		return err
	}
	store, ok := s.store.(state.GitLabOAuthStore)
	if !ok {
		return state.ErrInvalidArgument
	}
	_, err = store.PutGitLabOAuthConnection(r.Context(), state.GitLabOAuthConnection{AccountID: accountID,
		GitLabUserID: user.ID, Username: user.Username, CredentialsSealed: sealed, ExpiresAt: tokens.Expires})
	return err
}

func (s *server) getGitLabOAuthConnection(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in to read GitLab connection."))
		return
	}
	store, ok := s.store.(state.GitLabOAuthStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("GitLab OAuth storage is unavailable."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	connection, err := store.GitLabOAuthConnection(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not read GitLab connection."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "username": connection.Username, "gitlab_user_id": connection.GitLabUserID})
}

func (s *server) deleteGitLabOAuthConnection(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in to disconnect GitLab."))
		return
	}
	if err := s.disconnectGitLabOAuth(r, acct.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not disconnect GitLab."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) disconnectGitLabOAuth(r *http.Request, accountID string) error {
	store, ok := s.store.(state.GitLabOAuthStore)
	if !ok {
		return state.ErrInvalidArgument
	}
	if err := store.DeleteGitLabOAuthConnection(r.Context(), accountID); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	s.audit.Emit(r.Context(), "gitlab.oauth.disconnected", &accountID, nil)
	return nil
}

func (s *server) disconnectGitLabOAuthFromDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized", "Sign in to disconnect GitLab."))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, gitLabDisconnectAction, acct.ID, gitLabDisconnectCSRFCookie); err != nil {
		http.Redirect(w, r, "/dashboard/account?gitlab=disconnect-forbidden", http.StatusSeeOther)
		return
	}
	if err := s.disconnectGitLabOAuth(r, acct.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not disconnect GitLab."))
		return
	}
	http.Redirect(w, r, "/dashboard/account?gitlab=disconnected", http.StatusSeeOther)
}
