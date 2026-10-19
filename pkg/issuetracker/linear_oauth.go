package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type LinearOAuthConfig struct{ ClientID, ClientSecret, RedirectURI string }

func LoadLinearOAuthConfig(getenv func(string) string) (LinearOAuthConfig, error) {
	c := LinearOAuthConfig{getenv("FAAS_LINEAR_OAUTH_CLIENT_ID"), getenv("FAAS_LINEAR_OAUTH_CLIENT_SECRET"), getenv("FAAS_LINEAR_OAUTH_REDIRECT_URI")}
	if c == (LinearOAuthConfig{}) {
		return c, nil
	}
	u, err := url.Parse(c.RedirectURI)
	if validateToken(c.ClientID) != nil || validateToken(c.ClientSecret) != nil || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/oauth/linear/callback" || u.RawQuery != "" || u.Fragment != "" || len(c.RedirectURI) > api.IssueTrackerLinkURLMaxBytes {
		return LinearOAuthConfig{}, errors.New("configure FAAS_LINEAR_OAUTH_CLIENT_ID, FAAS_LINEAR_OAUTH_CLIENT_SECRET and an HTTPS FAAS_LINEAR_OAUTH_REDIRECT_URI ending in /oauth/linear/callback")
	}
	return c, nil
}
func (c LinearOAuthConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != ""
}
func (c LinearOAuthConfig) Authorize(state, challenge string) string {
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "response_type": {"code"}, "scope": {"read,write"}, "actor": {"user"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return "https://linear.app/oauth/authorize?" + q.Encode()
}

type LinearOAuthTokens struct {
	AccessToken, RefreshToken string
	ExpiresAt                 time.Time
}

var ErrLinearOAuthRejected = errors.New("linear OAuth grant was rejected; reconnect")

type LinearOAuthProvider interface {
	ExchangeLinearOAuth(context.Context, LinearOAuthConfig, string, string) (LinearOAuthTokens, error)
	RefreshLinearOAuth(context.Context, LinearOAuthConfig, string) (LinearOAuthTokens, error)
	RevokeLinearOAuth(context.Context, string) error
	ListLinearOAuthTeams(context.Context, string) (LinearOAuthWorkspace, error)
}

func (c *Client) ExchangeLinearOAuth(ctx context.Context, cfg LinearOAuthConfig, code, verifier string) (LinearOAuthTokens, error) {
	return c.linearOAuthToken(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "redirect_uri": {cfg.RedirectURI}, "code": {code}, "code_verifier": {verifier}})
}
func (c *Client) RefreshLinearOAuth(ctx context.Context, cfg LinearOAuthConfig, refresh string) (LinearOAuthTokens, error) {
	return c.linearOAuthToken(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "refresh_token": {refresh}})
}
func (c *Client) linearOAuthToken(ctx context.Context, form url.Values) (LinearOAuthTokens, error) {
	data, err := c.linearOAuthPost(ctx, "/oauth/token", form)
	if err != nil {
		return LinearOAuthTokens{}, err
	}
	var response struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		TokenType    string          `json:"token_type"`
		ExpiresIn    int64           `json:"expires_in"`
		Scope        json.RawMessage `json:"scope"`
	}
	if json.Unmarshal(data, &response) != nil || !strings.EqualFold(response.TokenType, "Bearer") || validateToken(response.AccessToken) != nil || validateToken(response.RefreshToken) != nil || response.ExpiresIn <= int64(api.IssueTrackerLinearOAuthRefreshSkew/time.Second) || response.ExpiresIn > int64(api.IssueTrackerLinearOAuthTokenMaxTTL/time.Second) {
		return LinearOAuthTokens{}, errors.New("linear OAuth token response is incomplete")
	}
	var scopes []string
	var scope string
	if json.Unmarshal(response.Scope, &scope) == nil {
		scopes = strings.Fields(scope)
	} else if json.Unmarshal(response.Scope, &scopes) != nil {
		return LinearOAuthTokens{}, errors.New("linear OAuth scopes could not be confirmed")
	}
	have := map[string]bool{}
	for _, s := range scopes {
		have[s] = true
	}
	if !have["read"] || !have["write"] {
		return LinearOAuthTokens{}, errors.New("linear OAuth requires read and write scopes")
	}
	return LinearOAuthTokens{response.AccessToken, response.RefreshToken, time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)}, nil
}
func (c *Client) linearOAuthPost(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.linear.app"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid Linear OAuth request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("linear OAuth request outcome could not be confirmed")
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, api.IssueTrackerResponseMaxBytes+1))
	if err != nil || len(data) > api.IssueTrackerResponseMaxBytes {
		return nil, errors.New("linear OAuth response is unreadable")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if (res.StatusCode == 400 || res.StatusCode == 401) && (failure.Error == "invalid_grant" || failure.Error == "invalid_token") {
			return nil, ErrLinearOAuthRejected
		}
		return nil, errors.New("linear OAuth request failed")
	}
	return data, nil
}
func (c *Client) RevokeLinearOAuth(ctx context.Context, refresh string) error {
	_, err := c.linearOAuthPost(ctx, "/oauth/revoke", url.Values{"token": {refresh}, "token_type_hint": {"refresh_token"}})
	return err
}

// OAuthAuthorization is an internal authorization value, never an API key input.
func OAuthAuthorization(token string) string { return "Bearer " + token }

type ProviderUnauthorized struct{ Provider string }

func (e *ProviderUnauthorized) Error() string { return e.Provider + " access was rejected" }
