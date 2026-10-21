package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A token is fetched again this long before it expires, so a request that
// carries it still finds it valid.
const credentialsRefreshMargin = time.Minute

var (
	serviceAccountID     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	serviceAccountSecret = regexp.MustCompile(`^gasa_[A-Za-z0-9_-]{43}$`)
)

// ClientCredentials gets access tokens for an organization's service
// account with the client-credentials grant, for a backend that calls an
// API or the Data API as that organization. Token caches one until a minute
// before it expires; concurrent callers share one request. It is safe for
// concurrent use.
//
//	machine, err := auth.NewClientCredentials("https://shop-auth.gregale.app", clientID, clientSecret)
//	token, err := machine.Token(ctx)
//	req.Header.Set("Authorization", "Bearer "+token)
type ClientCredentials struct {
	tokenURL string
	id       string
	secret   string
	scope    []string
	http     *http.Client
	now      func() time.Time

	mu        sync.Mutex
	token     string
	refreshAt time.Time
}

// CredentialsOption configures ClientCredentials.
type CredentialsOption func(*ClientCredentials)

// WithScope narrows tokens to these permissions; each must be one the
// account's role holds.
func WithScope(permissions ...string) CredentialsOption {
	return func(c *ClientCredentials) { c.scope = permissions }
}

// WithCredentialsHTTPClient sets the client used to reach the Auth app.
// Redirects are never followed.
func WithCredentialsHTTPClient(hc *http.Client) CredentialsOption {
	return func(c *ClientCredentials) { c.http = hc }
}

// NewClientCredentials returns a token source for the service account
// clientID with one of its secrets, at the Auth app whose origin is authURL.
func NewClientCredentials(authURL, clientID, clientSecret string, opts ...CredentialsOption) (*ClientCredentials, error) {
	origin, err := authOrigin(authURL)
	if err != nil {
		return nil, err
	}
	if !serviceAccountID.MatchString(clientID) {
		return nil, errors.New("auth: clientID must be a service account ID")
	}
	if !serviceAccountSecret.MatchString(clientSecret) {
		return nil, errors.New("auth: clientSecret must be a service account secret")
	}
	c := &ClientCredentials{tokenURL: origin + "/auth/v1/oauth/token", id: clientID, secret: clientSecret, http: http.DefaultClient, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil {
		return nil, errors.New("auth: invalid option")
	}
	noRedirects := *c.http
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.http = &noRedirects
	return c, nil
}

// Token returns a valid access token, fetching a new one when needed. A
// refusal is an *Error with the OAuth error code, such as invalid_client.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.refreshAt) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	if len(c.scope) > 0 {
		form.Set("scope", strings.Join(c.scope, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(url.QueryEscape(c.id), url.QueryEscape(c.secret))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var out struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		Error       string  `json:"error"`
	}
	_ = json.Unmarshal(data, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" || out.ExpiresIn <= 0 {
		code := out.Error
		if code == "" {
			code = "http_" + fmt.Sprint(resp.StatusCode)
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			status = http.StatusBadGateway
		}
		return "", &Error{Status: status, Code: code}
	}
	c.token = out.AccessToken
	c.refreshAt = c.now().Add(time.Duration(out.ExpiresIn*float64(time.Second)) - credentialsRefreshMargin)
	return c.token, nil
}

// Clear drops the cached token, for example after an API answered 401.
func (c *ClientCredentials) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
}
