package gitlabsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const oauthURL = "https://gitlab.com/oauth"

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// OAuthURL is used by isolated tests only.
	OAuthURL string
	HTTP     *http.Client
}

type Tokens struct {
	Access  string
	Refresh string
	Expires time.Time
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (c OAuthConfig) endpoint() string {
	if c.OAuthURL != "" {
		return strings.TrimRight(c.OAuthURL, "/")
	}
	return oauthURL
}

func (c OAuthConfig) Valid() bool {
	if c.ClientID == "" || c.ClientSecret == "" || strings.ContainsAny(c.ClientID+c.ClientSecret, "\r\n") {
		return false
	}
	u, err := url.Parse(c.RedirectURI)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func (c OAuthConfig) AuthorizationURL(state, challenge string) (string, error) {
	if !c.Valid() || len(state) < 32 || strings.ContainsAny(state, "\r\n") || len(challenge) < 43 {
		return "", ErrUnavailable
	}
	u, err := url.Parse(c.endpoint() + "/authorize")
	if err != nil {
		return "", ErrUnavailable
	}
	q := u.Query()
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "read_api read_repository")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c OAuthConfig) Exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\r\n") || len(verifier) < 43 || len(verifier) > 128 {
		return Tokens{}, ErrUnavailable
	}
	return c.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.RedirectURI}, "code_verifier": {verifier}})
}

func (c OAuthConfig) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	if refresh == "" || len(refresh) > 4096 || strings.ContainsAny(refresh, "\r\n") {
		return Tokens{}, ErrUnavailable
	}
	return c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "redirect_uri": {c.RedirectURI}})
}

func (c OAuthConfig) token(ctx context.Context, values url.Values) (Tokens, error) {
	if !c.Valid() {
		return Tokens{}, ErrUnavailable
	}
	values.Set("client_id", c.ClientID)
	values.Set("client_secret", c.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint()+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return Tokens{}, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := Client{HTTP: c.HTTP}.client()
	client.Timeout = 10 * time.Second
	response, err := client.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: token request failed", ErrUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("%w: token status %d", ErrUnavailable, response.StatusCode)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&body) != nil || body.AccessToken == "" || body.RefreshToken == "" || body.ExpiresIn < 60 || body.ExpiresIn > 86400 || !strings.EqualFold(body.TokenType, "Bearer") {
		return Tokens{}, ErrUnavailable
	}
	// GitLab's documented authorization-code and refresh responses may omit
	// scope. When present it must retain both read-only grants.
	if body.Scope != "" {
		scopes := strings.Fields(body.Scope)
		if !containsScope(scopes, "read_api") || !containsScope(scopes, "read_repository") {
			return Tokens{}, ErrUnavailable
		}
	}
	return Tokens{Access: body.AccessToken, Refresh: body.RefreshToken, Expires: time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)}, nil
}

func containsScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}

func (c Client) CurrentUser(ctx context.Context, token string) (User, error) {
	response, err := c.get(ctx, token, "/user")
	if err != nil {
		return User{}, err
	}
	defer func() { _ = response.Body.Close() }()
	var user User
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&user) != nil || user.ID <= 0 || user.Username == "" {
		return User{}, ErrUnavailable
	}
	return user, nil
}

func (u User) StringID() string { return strconv.FormatInt(u.ID, 10) }
