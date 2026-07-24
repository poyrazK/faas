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
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	googleAuthStateCookie = "faas_google_state"
	googleAuthPath        = "/v1/auth/google"
	googleCallbackPath    = "/v1/auth/google/callback"
	// schemeHTTPS is the value of the X-Forwarded-Proto header (and the
	// tld of the redirect scheme) when the request was served over TLS.
	// Lifted to a const so goconst doesn't flag the repeated literal.
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
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		s.log.Error("GOOGLE_CLIENT_ID environment variable is not configured")
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "google_oauth_misconfigured", "OAuth Misconfigured", "GOOGLE_CLIENT_ID environment variable is required"))
		return
	}

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

	redirectURI := os.Getenv("GOOGLE_REDIRECT_URI")
	if redirectURI == "" {
		host := r.Host
		scheme := "http"
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
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_state", "Invalid State", "missing CSRF state cookie"))
		return
	}

	queryState := r.URL.Query().Get("state")
	if queryState == "" || queryState != stateCookie.Value {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "csrf_mismatch", "CSRF Error", "state token mismatch"))
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "missing_code", "Authorization Error", "missing code parameter from Google"))
		return
	}

	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "google_oauth_misconfigured", "OAuth Misconfigured", "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required"))
		return
	}

	redirectURI := os.Getenv("GOOGLE_REDIRECT_URI")
	if redirectURI == "" {
		host := r.Host
		scheme := "http"
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

	// Provision or fetch account
	acct, err := s.provisionOrFetchGoogleAccount(r.Context(), googleUser.Email)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "Account Error", err.Error()))
		return
	}

	// Issue Session Cookie
	cookie, err := s.sessions.Issue(acct.ID)
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

	redirectTarget := os.Getenv("WEBSITE_URL")
	if redirectTarget == "" {
		redirectTarget = "/"
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

func (s *server) provisionOrFetchGoogleAccount(ctx context.Context, email string) (state.Account, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	acct, err := s.store.AccountByEmail(ctx, email)
	if err == nil {
		return acct, nil
	}

	// Account does not exist yet: Provision new account
	return s.store.CreateAccount(ctx, email, api.PlanFree)
}
