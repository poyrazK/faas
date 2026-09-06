// GitHub OAuth dashboard login (issue #165 PR #2, ADR-032).
//
// This is the dashboard LOGIN surface — a sibling of the existing
// Google /v1/auth/google flow. It is NOT the GitHub App install-bind
// flow at /oauth/callback (cmd/apid/handlers_oauth.go), which is
// unrelated to dashboard auth and lives behind sessionAuth.
//
// Flow:
//  1. GET /v1/auth/github → sets a 16-byte CSRF state cookie scoped
//     to /v1/auth/github/callback and redirects to github.com/login/
//     oauth/authorize with the state + scope=user:email.
//  2. GET /v1/auth/github/callback → verifies the state cookie ==
//     query state, exchanges the code at github.com/login/oauth/
//     access_token (Accept: application/json variant), fetches the
//     /user profile (id + login + name + avatar), fetches /user/
//     emails and filters to primary && verified, then runs the
//     same provisionOrFetchOAuth helper the Google flow uses
//     (sub-first lookup via oauth_links), mints a session cookie,
//     and redirects to WEBSITE_URL or /.
//
// The state cookie is scoped to /v1/auth/github/callback so a
// parallel Google OAuth flow can't accidentally leak the GitHub
// state. Spec §11: every OAuth callback MUST enforce
// email_verified (Google) or primary && verified (GitHub) before
// minting a session — unverified emails are rejected.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	githubAuthStateCookie = "faas_github_state"
	githubAuthPath        = "/v1/auth/github"
	githubCallbackPath    = "/v1/auth/github/callback"

	// githubAuthScope is the OAuth scope we ask for. We need
	// "read:user" for the /user profile (id, login, name) and
	// "user:email" for the /user/emails endpoint (verified email
	// discovery). The flow MUST fetch /user/emails rather than
	// trust the unauthenticated email field on /user — the
	// /user response is null until the user sets a public email,
	// and even when present the email is not necessarily verified.
	githubAuthScope = "read:user user:email"
)

// GitHubUserInfo is the /user profile payload. We only need the
// fields the dashboard chrome uses — id for the oauth_links.sub
// primary key, login for the dashboard's "signed in as <login>"
// display, name + avatar for the chrome.
type GitHubUserInfo struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email"` // may be null
}

// GitHubEmail is one row of the /user/emails payload.
type GitHubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// renderGitHubAuthRedirect (GET /v1/auth/github)
// Redirects the user to GitHub's OAuth authorize endpoint.
func (s *server) renderGitHubAuthRedirect(w http.ResponseWriter, r *http.Request) {
	// Issue #419 / ADR-046: same boot-resolved SignInConfig guard
	// as the Google flow. Half-set configs fail to start at boot;
	// the Disabled case below is the operator-chose-not-to-ship-it
	// path.
	if !s.oauthConfig.GitHub.Enabled() {
		s.disabledOAuthResponse(w, auth.GitHubProviderName,
			"GITHUB_CLIENT_ID/GITHUB_CLIENT_SECRET unset",
			"GitHub sign-in is not configured on this host. Set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET in /etc/faas/sealed.env and restart.")
		return
	}
	clientID := s.oauthConfig.GitHub.ClientID

	stateTokenBytes := make([]byte, 16)
	if _, err := rand.Read(stateTokenBytes); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to generate CSRF state"))
		return
	}
	stateToken := hex.EncodeToString(stateTokenBytes)

	http.SetCookie(w, &http.Cookie{
		Name:     githubAuthStateCookie,
		Value:    stateToken,
		Path:     githubCallbackPath,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})

	redirectURI := s.oauthConfig.GitHub.RedirectURI
	if redirectURI == "" {
		host := r.Host
		scheme := schemeHTTP
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
			scheme = schemeHTTPS
		}
		redirectURI = fmt.Sprintf("%s://%s%s", scheme, host, githubCallbackPath)
	}

	githubAuthURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&scope=%s&allow_signup=true",
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(stateToken),
		url.QueryEscape(githubAuthScope),
	)
	http.Redirect(w, r, githubAuthURL, http.StatusFound)
}

// handleGitHubOAuthCallback (GET /v1/auth/github/callback)
// Verifies state, exchanges the code, fetches the profile + verified
// email, and signs the user in.
func (s *server) handleGitHubOAuthCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(githubAuthStateCookie)
	if err != nil || stateCookie.Value == "" {
		// Issue #286: OAuth callback failure path that lands
		// before any customer-supplied identity is known — the
		// audit row carries email_hash="" (the discriminator is
		// ip + user_agent). See the Google callback for the
		// rationale.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_state", "Invalid State", "missing CSRF state cookie"))
		return
	}
	queryState := r.URL.Query().Get("state")
	if queryState == "" || queryState != stateCookie.Value {
		// Issue #286: CSRF mismatch — see note above.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "csrf_mismatch", "CSRF Error", "state token mismatch"))
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		// Issue #286: GitHub consent-denial path — the user
		// backed out before granting the scopes. Same call
		// shape as the other OAuth denial paths.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "missing_code", "Authorization Error", "missing code parameter from GitHub"))
		return
	}

	// Issue #419 / ADR-046: same guard as the consent redirect.
	// The callback should never be reached if the provider is
	// disabled, but a 503 here is the correct shape if a stale
	// cookie or direct callback hit slips past the dashboard's
	// disabled-button gating.
	if !s.oauthConfig.GitHub.Enabled() {
		s.disabledOAuthResponse(w, auth.GitHubProviderName,
			"GITHUB_CLIENT_ID/GITHUB_CLIENT_SECRET unset",
			"GitHub sign-in is not configured on this host. Set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET in /etc/faas/sealed.env and restart.")
		return
	}
	clientID := s.oauthConfig.GitHub.ClientID
	clientSecret := s.oauthConfig.GitHub.ClientSecret

	redirectURI := s.oauthConfig.GitHub.RedirectURI
	if redirectURI == "" {
		host := r.Host
		scheme := schemeHTTP
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
			scheme = schemeHTTPS
		}
		redirectURI = fmt.Sprintf("%s://%s%s", scheme, host, githubCallbackPath)
	}

	// Exchange the code for an access token. The Accept:
	// application/json variant is documented at
	// https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
	// — without it the response is form-encoded and the JWT-style
	// token is buried in the body text. http.PostForm cannot set
	// request headers, so build the request explicitly with the
	// Accept header before issuing the POST.
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	tokenReq, err := http.NewRequestWithContext(r.Context(),
		http.MethodPost, "https://github.com/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		s.log.Error("github oauth token exchange build", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to build token exchange request"))
		return
	}
	tokenReq.Header.Set("Accept", "application/json")
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		s.log.Error("github oauth token exchange failed", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable", "GitHub Unreachable", "token exchange failed"))
		return
	}
	defer func() { _ = tokenResp.Body.Close() }()

	// Read the body first so we can decide whether to parse JSON
	// (the Accept header may not have stuck — older GitHub OAuth
	// apps respond with form-encoded).
	bodyBytes, _ := io.ReadAll(tokenResp.Body)
	accessToken := parseGitHubAccessToken(bodyBytes)
	if accessToken == "" {
		s.log.Error("github oauth token exchange no access_token", "status", tokenResp.StatusCode, "body", string(bodyBytes))
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "oauth_exchange_failed", "OAuth Failed", "failed to obtain access token from GitHub"))
		return
	}

	// Fetch the /user profile.
	githubUser, err := fetchGitHubUser(r.Context(), accessToken)
	if err != nil {
		s.log.Error("github userinfo fetch failed", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable", "GitHub Unreachable", "failed to fetch user info from GitHub"))
		return
	}

	// Fetch the verified primary email. GitHub's /user profile
	// returns email=null when the customer has no public email;
	// the /user/emails endpoint returns the full list filtered
	// to primary && verified. Spec §11: NEVER mint a session on
	// an unverified email.
	email, err := fetchGitHubVerifiedPrimaryEmail(r.Context(), accessToken)
	if err != nil {
		s.log.Error("github emails fetch failed", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable", "GitHub Unreachable", "failed to fetch verified email from GitHub"))
		return
	}
	if email == "" {
		api.WriteProblem(w, api.ErrEmailNotVerified("github"))
		return
	}

	// Provision or fetch the account. Sub-first lookup via the
	// oauth_links table mirrors the Google handler — the
	// §11 anti-takeover invariant is the same regardless of the
	// provider. GitHub's user id is int64; the oauth_links PK is
	// keyed on the string form (decimal).
	acct, err := s.provisionOrFetchOAuthAccount(r.Context(), "github", githubUserEmailID(githubUser), email, githubUser)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Account Error", err.Error()))
		return
	}

	// Mint session cookie.
	//
	// IAM-2: stamp mfa_pending=true if the account has the policy
	// flag set but has not yet enrolled. Same flip as
	// handlers_google.go; see the comment there.
	//
	// PR-B §11 ownership proof: the dashboard session must carry the
	// GitHub login we just verified, so the /oauth/callback handler
	// can compare it to the install's account.login. The IAM-3 path
	// seals the envelope with mfa_pending AND github_login AND sid in
	// a single crypto round (no double-seal). The cookie shape is
	// unchanged for callers that don't read any of the three fields
	// (all three JSON tags are `omitempty`).
	//
	// IAM-3 (ADR-039) goes through the unified helper so the sessions
	// row is created + auth.session.created is emitted.
	cookie, _, err := s.issueDashboardSessionWithGithub(r.Context(), r, acct.ID, mfaSessionPending(acct), "github", githubUser.Login)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Session Error", err.Error()))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionCookieLifetime.Seconds()),
	})

	s.log.Info("github oauth sign-in successful", "login", logsanitize.Field(githubUser.Login), "account_id", acct.ID)

	// IAM-4 (ADR-035): record the auth.login success. Mirrors the
	// existing slog line + the Google handler at handlers_google.go:
	// 228-231 so operators correlating slog and audit see one
	// identifier. data.login carries the GitHub username (Google has
	// no equivalent — Google's identity display name comes from the
	// /userinfo "name" field which is not a stable handle).
	s.audit.Emit(r.Context(), "auth.login", &acct.ID, map[string]any{
		"method": "github",
		"email":  email,
		"login":  githubUser.Login,
	})

	redirectTarget := os.Getenv("WEBSITE_URL")
	if redirectTarget == "" {
		redirectTarget = "/"
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

// githubUserEmailID converts a GitHub user-id (int64) into the
// decimal string form used by the oauth_links.provider_subject
// column. The cast is unambiguous: GitHub's id is a server-issued
// monotonically increasing integer, and the decimal string is the
// canonical wire form.
func githubUserEmailID(u GitHubUserInfo) string {
	return fmt.Sprintf("%d", u.ID)
}

// parseGitHubAccessToken extracts the access_token from a
// GitHub OAuth token-exchange response. The modern Accept:
// application/json variant returns a JSON body; older clients
// receive a form-encoded body. We accept both.
func parseGitHubAccessToken(body []byte) string {
	bodyStr := strings.TrimSpace(string(body))
	if strings.HasPrefix(bodyStr, "{") {
		var resp struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(body, &resp); err == nil {
			return resp.AccessToken
		}
	}
	// Form-encoded fallback.
	values, err := url.ParseQuery(bodyStr)
	if err != nil {
		return ""
	}
	return values.Get("access_token")
}

// fetchGitHubUser GETs /user with the access token. Returns the
// parsed profile or an error if the request / decode failed.
func fetchGitHubUser(ctx context.Context, accessToken string) (GitHubUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return GitHubUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "faas-oidc-login")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return GitHubUserInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return GitHubUserInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var info GitHubUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return GitHubUserInfo{}, err
	}
	return info, nil
}

// fetchGitHubVerifiedPrimaryEmail GETs /user/emails and returns
// the primary && verified email, or "" if no such email exists.
// We never accept an unverified email — spec §11: only verified
// emails mint sessions.
func fetchGitHubVerifiedPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "faas-oidc-login")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var emails []GitHubEmail
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", nil
}

// provisionOrFetchOAuthAccount is the shared §11 anti-takeover
// closure for OAuth-based dashboard login. It runs:
//
//  1. Sub-first lookup against oauth_links. A hit returns the
//     linked account (the first party to bind the sub owns the
//     row; a later password-account with the same email cannot
//     claim it).
//  2. Email-based lookup. If a legacy account existed at the email
//     (pre-OAuth creation), bind the link and return it.
//  3. Fresh account on the Free plan + link.
//
// The Google flow calls this with info.ProviderID (or equivalently
// the GoogleUserInfo.ID), the GitHub flow with the GitHubUserInfo.ID
// cast to string. The sub is what the oauth_links PK is keyed on.
func (s *server) provisionOrFetchOAuthAccount(ctx context.Context, provider, sub, email string, _ any) (state.Account, error) {
	email = strings.TrimSpace(strings.ToLower(email))

	// 1. Sub-first lookup.
	if link, err := s.store.OAuthLinkByProviderSubject(ctx, provider, sub); err == nil {
		if acct, err := s.store.AccountByID(ctx, link.AccountID); err == nil {
			return s.verifyOAuthAccountEmail(ctx, acct)
		}
		// Link references a missing account: fall through to
		// email-based recovery. The link stays orphaned or will be
		// re-bound below.
	}

	// 2. Email-based lookup (legacy "user had a password account,
	//    now signs in with OAuth").
	acct, err := s.store.AccountByEmail(ctx, email)
	if err == nil {
		if err := s.store.UpsertOAuthLink(ctx, acct.ID, provider, sub, email, true); err != nil {
			s.log.Error("oauth.upsert_link", "provider", provider, "err", err)
		}
		return s.verifyOAuthAccountEmail(ctx, acct)
	}

	// 3. Fresh account.
	res, err := s.store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: email,
		Plan:  api.PlanFree,
	})
	if err != nil {
		return state.Account{}, err
	}
	if err := s.store.UpsertOAuthLink(ctx, res.Account.ID, provider, sub, email, true); err != nil {
		s.log.Error("oauth.upsert_link", "provider", provider, "err", err)
	}
	return res.Account, nil
}
