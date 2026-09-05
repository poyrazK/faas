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
	googleAuthStateCookie = "faas_google_state"
	googleAuthPath        = "/v1/auth/google"
	googleCallbackPath    = "/v1/auth/google/callback"
	// schemeHTTP + schemeHTTPS are the URL schemes used in the OAuth
	// redirect / domain helper. Lifted to package-level consts so
	// goconst doesn't flag the repeated literals across the auth
	// handlers (handlers_github.go, handlers_auth_login.go).
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// GoogleUserInfo represents the payload returned by Google's OAuth2 userinfo endpoint.
type GoogleUserInfo struct {
	ID            string `json:"sub"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// renderGoogleAuthRedirect (GET /v1/auth/google)
// Redirects user to Google OAuth 2.0 consent screen.
func (s *server) renderGoogleAuthRedirect(w http.ResponseWriter, r *http.Request) {
	// Issue #419 / ADR-046: the boot-resolved SignInConfig is the
	// only source of truth for whether Google OAuth is configured.
	// Half-set configs fail to start at boot (cmd/apid/main.go); the
	// Disabled case below is the operator-chose-not-to-ship-it path.
	if !s.oauthConfig.Google.Enabled() {
		s.disabledOAuthResponse(w, auth.GoogleProviderName,
			"GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET unset",
			"Google sign-in is not configured on this host. Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in /etc/faas/sealed.env and restart.")
		return
	}
	clientID := s.oauthConfig.Google.ClientID

	// Real Google OAuth 2.0 flow
	stateTokenBytes := make([]byte, 16)
	if _, err := rand.Read(stateTokenBytes); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to generate CSRF state"))
		return
	}
	stateToken := hex.EncodeToString(stateTokenBytes)

	// Set CSRF Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     googleAuthStateCookie,
		Value:    stateToken,
		Path:     googleCallbackPath,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})

	redirectURI := s.oauthConfig.Google.RedirectURI
	if redirectURI == "" {
		host := r.Host
		scheme := schemeHTTP
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
			scheme = schemeHTTPS
		}
		redirectURI = fmt.Sprintf("%s://%s%s", scheme, host, googleCallbackPath)
	}

	googleAuthURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid%%20email%%20profile&state=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(stateToken),
	)

	http.Redirect(w, r, googleAuthURL, http.StatusFound)
}

// handleGoogleOAuthCallback (GET /v1/auth/google/callback)
// Verifies state token, exchanges OAuth code for Google user profile, and signs user in.
func (s *server) handleGoogleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(googleAuthStateCookie)
	if err != nil || stateCookie.Value == "" {
		// Issue #286: the OAuth callback can fail before any
		// identity is known — there is no customer-supplied email
		// to hash, so the audit row carries email_hash="". The IP
		// + user_agent discriminator still lets SOC 2 evidence
		// correlate a credential-stuffing pattern of GETs against
		// the callback URL.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_state", "Invalid State", "missing CSRF state cookie"))
		return
	}

	queryState := r.URL.Query().Get("state")
	if queryState == "" || queryState != stateCookie.Value {
		// Issue #286: see note above.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "csrf_mismatch", "CSRF Error", "state token mismatch"))
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		// Issue #286: see note above. The OAuth consent screen
		// denial path lands here — `code` is empty because the
		// user backed out before granting the scopes.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			"", r.UserAgent())
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "missing_code", "Authorization Error", "missing code parameter from Google"))
		return
	}

	// Issue #419 / ADR-046: same guard as the consent redirect —
	// the callback should never be reached if the provider is
	// disabled, but a 503 here is the correct shape if a stale
	// cookie or direct callback hit slips past the dashboard's
	// disabled-button gating. The auth-result audit row is
	// emitted at the existing EmitFailedLogin call sites above;
	// no extra audit is needed on the disabled path because we
	// have no email yet to hash.
	if !s.oauthConfig.Google.Enabled() {
		s.disabledOAuthResponse(w, auth.GoogleProviderName,
			"GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET unset",
			"Google sign-in is not configured on this host. Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in /etc/faas/sealed.env and restart.")
		return
	}
	clientID := s.oauthConfig.Google.ClientID
	clientSecret := s.oauthConfig.Google.ClientSecret

	redirectURI := s.oauthConfig.Google.RedirectURI
	if redirectURI == "" {
		host := r.Host
		scheme := schemeHTTP
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
			scheme = schemeHTTPS
		}
		redirectURI = fmt.Sprintf("%s://%s%s", scheme, host, googleCallbackPath)
	}

	// Exchange Code for Access Token
	tokenResp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		s.log.Error("google oauth token exchange failed", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "google_unreachable", "Google Unreachable", "token exchange failed"))
		return
	}
	defer func() { _ = tokenResp.Body.Close() }()

	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		s.log.Error("google oauth token exchange non-200", "status", tokenResp.StatusCode, "body", string(body))
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "oauth_exchange_failed", "OAuth Failed", "failed to obtain access token from Google"))
		return
	}

	var tokenData struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "json_error", "Internal Error", "failed to parse Google token response"))
		return
	}

	// Fetch Google User Profile
	userInfoReq, err := http.NewRequestWithContext(r.Context(), "GET", "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", err.Error()))
		return
	}
	userInfoReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)

	userInfoResp, err := http.DefaultClient.Do(userInfoReq)
	if err != nil || userInfoResp.StatusCode != http.StatusOK {
		s.log.Error("google userinfo fetch failed", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "google_unreachable", "Google Unreachable", "failed to fetch user info from Google"))
		return
	}
	defer func() { _ = userInfoResp.Body.Close() }()

	var googleUser GoogleUserInfo
	if err := json.NewDecoder(userInfoResp.Body).Decode(&googleUser); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "json_error", "Internal Error", "failed to decode Google user info"))
		return
	}

	if googleUser.Email == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "email_missing", "Missing Email", "Google profile did not contain an email address"))
		return
	}

	// Enforce email_verified (issue #165 PR #2, ADR-032). The pre-#165
	// handler parsed VerifiedEmail but never checked it; a Google
	// account with an unverified primary email could mint a session.
	// We never mint an unverified session — the customer can verify
	// the email on Google's side and retry.
	if !googleUser.VerifiedEmail {
		api.WriteProblem(w, api.ErrEmailNotVerified("google"))
		return
	}

	// Provision or fetch account. The full googleUser struct is
	// passed so the helper can do a sub-first lookup against the
	// oauth_links table — the §11 anti-takeover invariant
	// (one OAuth subject binds to one account, period).
	acct, err := s.provisionOrFetchGoogleAccount(r.Context(), googleUser)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Account Error", err.Error()))
		return
	}

	// Issue Session Cookie. IAM-2 mfa_pending stamp preserved;
	// IAM-3 (ADR-039) mints a sid + creates the sessions row +
	// emits auth.session.created via the unified helper.
	cookie, _, err := s.issueDashboardSession(r.Context(), r, acct.ID, mfaSessionPending(acct), "google")
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

	s.log.Info("google oauth sign-in successful", "email", logsanitize.Field(googleUser.Email), "account_id", acct.ID)

	// IAM-4 (ADR-035): record the auth.login success. Mirrors the
	// existing slog line: the audit row carries the same email so
	// operators correlating slog and audit see one identifier.
	s.audit.Emit(r.Context(), "auth.login", &acct.ID, map[string]any{
		"method": "google",
		"email":  googleUser.Email,
	})

	redirectTarget := os.Getenv("WEBSITE_URL")
	if redirectTarget == "" {
		redirectTarget = "/"
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

func (s *server) provisionOrFetchGoogleAccount(ctx context.Context, info GoogleUserInfo) (state.Account, error) {
	email := strings.TrimSpace(strings.ToLower(info.Email))

	// Sub-first lookup (spec §11 anti-takeover invariant, issue #165
	// PR #2). The (provider, sub) composite is the primary key on
	// oauth_links; the first party to bind a sub owns the row.
	// A later password-account with the same email cannot claim
	// the link — see pkg/state/pgstore.go::UpsertOAuthLink.
	if link, err := s.store.OAuthLinkByProviderSubject(ctx, "google", info.ID); err == nil {
		acct, err := s.store.AccountByID(ctx, link.AccountID)
		if err == nil {
			return s.verifyOAuthAccountEmail(ctx, acct)
		}
		// Link references a missing account: fall through to
		// email-based recovery. The link was created against a
		// deleted account; the row stays orphaned or will be
		// re-bound below.
	}

	// Sub not bound. Check email — if a legacy account (pre-#165,
	// pre-OAuth) already exists at this email, we bind the link to
	// it. This is the "user had a password account, then signs in
	// with Google" case.
	acct, err := s.store.AccountByEmail(ctx, email)
	if err == nil {
		// Bind the link to the existing account. UpsertOAuthLink
		// returns ErrConflict on a different-account re-bind
		// (handled by the sub-first lookup above), so this is
		// safe.
		if err := s.store.UpsertOAuthLink(ctx, acct.ID, "google", info.ID, email, info.VerifiedEmail); err != nil {
			s.log.Error("google.upsert_link", "err", err)
		}
		return s.verifyOAuthAccountEmail(ctx, acct)
	}

	// Neither sub nor email is bound: create a fresh account on
	// the Free plan and bind the link.
	res, err := s.store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: email,
		Plan:  api.PlanFree,
	})
	if err != nil {
		return state.Account{}, err
	}
	if err := s.store.UpsertOAuthLink(ctx, res.Account.ID, "google", info.ID, email, info.VerifiedEmail); err != nil {
		// The link is the §11 invariant. Log but don't fail the
		// sign-in — the customer can re-trigger the bind on the
		// next login.
		s.log.Error("google.upsert_link", "err", err)
	}
	return res.Account, nil
}
