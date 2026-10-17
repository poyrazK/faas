package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func (c *Client) ListHostingOIDCTrusts(ctx context.Context, site string) ([]HostingOIDCTrust, error) {
	var out []HostingOIDCTrust
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/oidc-trusts", nil, &out)
	return out, err
}
func (c *Client) CreateHostingOIDCTrust(ctx context.Context, site string, rules HostingOIDCTrustRules) (HostingOIDCTrust, error) {
	var out HostingOIDCTrust
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, hostingSitePath(site)+"/oidc-trusts", rules, &out)
	return out, err
}
func (c *Client) RevokeHostingOIDCTrust(ctx context.Context, site, id string) error {
	return c.doWithoutIdempotencyKey(ctx, http.MethodDelete, hostingSitePath(site)+"/oidc-trusts/"+url.PathEscape(id), nil, nil)
}

// NewGitHubHostingOIDCClient renews before API requests, never by replaying an
// allocation or a failed mutation. GitHub credentials and Gregale credentials
// travel through separate clients; neither client follows redirects.
func NewGitHubHostingOIDCClient(base, site, trust, requestURL, requestToken string) (*Client, error) {
	origin, err := url.Parse(base)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Opaque != "" || origin.Path != "" && origin.Path != "/" || uuid.Validate(site) != nil || uuid.Validate(trust) != nil || requestToken == "" {
		return nil, errors.New("OIDC requires an HTTPS API origin, site/trust UUIDs and GitHub job identity permission")
	}
	u, err := url.Parse(requestURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".actions.githubusercontent.com") || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return nil, errors.New("GitHub OIDC request endpoint must be an HTTPS actions.githubusercontent.com subdomain")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid GitHub OIDC request endpoint")
	}
	query.Set("audience", HostingOIDCAudience(site))
	u.RawQuery = query.Encode()
	base = strings.TrimSuffix(base, "/")
	privateHTTP := &http.Client{Transport: newClientTransport(), Timeout: HostingOIDCRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	source := &hostingGitHubSource{base: base, site: site, trust: trust, requestURL: u.String(), requestToken: requestToken, http: privateHTTP}
	c := NewHostingAutomationClient(base, "")
	c.http.Transport = &hostingOIDCTransport{next: c.http.Transport, source: source, origin: origin, site: site}
	return c, nil
}

type hostingGitHubSource struct {
	base, site, trust, requestURL, requestToken string
	http                                        *http.Client
}

func (s *hostingGitHubSource) credential(ctx context.Context) (HostingAutomationCredentialResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, HostingOIDCRequestTimeout)
	defer cancel()
	job, err := http.NewRequestWithContext(ctx, http.MethodGet, s.requestURL, nil)
	if err != nil {
		return HostingAutomationCredentialResponse{}, errors.New("GitHub OIDC request unavailable")
	}
	job.Header.Set("Authorization", "Bearer "+s.requestToken)
	var identity struct {
		Value string `json:"value"`
	}
	if !s.readJSON(job, &identity) || identity.Value == "" {
		return HostingAutomationCredentialResponse{}, errors.New("GitHub job identity unavailable; require id-token: write")
	}
	body, _ := json.Marshal(HostingOIDCExchangeRequest{SiteID: s.site, TrustID: s.trust, Assertion: identity.Value})
	exchange, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/v1/internal/hosting/oidc/exchange", bytes.NewReader(body))
	if err != nil {
		return HostingAutomationCredentialResponse{}, errors.New("site identity exchange unavailable")
	}
	exchange.Header.Set("Content-Type", "application/json")
	var out HostingAutomationCredentialResponse
	if !s.readJSON(exchange, &out) || !ValidHostingAutomationToken(out.Token) || out.Credential.SiteID != s.site || out.Credential.OIDCTrustID != s.trust || len(out.Credential.Operations) != 2 || !out.Credential.ExpiresAt.After(time.Now().Add(HostingOIDCRefreshMargin)) || out.Credential.ExpiresAt.After(time.Now().Add(HostingOIDCBearerTTL)) {
		return HostingAutomationCredentialResponse{}, errors.New("site identity exchange rejected or returned an invalid credential")
	}
	ops := map[string]bool{}
	for _, op := range out.Credential.Operations {
		ops[op] = true
	}
	if !ops["stage"] || !ops["preview"] {
		return HostingAutomationCredentialResponse{}, errors.New("site identity exchange returned unexpected authority")
	}
	return out, nil
}

func (s *hostingGitHubSource) readJSON(req *http.Request, out any) bool {
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, HostingOIDCRequestMaxBytes+1))
	return err == nil && len(data) <= HostingOIDCRequestMaxBytes && json.Unmarshal(data, out) == nil
}

type hostingOIDCTransport struct {
	next    http.RoundTripper
	source  *hostingGitHubSource
	origin  *url.URL
	site    string
	mu      sync.Mutex
	current HostingAutomationCredentialResponse
}

func (t *hostingOIDCTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	root := "/v1/internal/hosting/sites/" + t.site
	if req.URL.Scheme != t.origin.Scheme || req.URL.Host != t.origin.Host || req.URL.User != nil || req.URL.EscapedPath() != req.URL.Path || req.URL.Path != "/v1/internal/hosting/capabilities" && req.URL.Path != root && !strings.HasPrefix(req.URL.Path, root+"/") {
		return nil, errors.New("OIDC credential destination is outside the selected site")
	}
	t.mu.Lock()
	if !t.current.Credential.ExpiresAt.After(time.Now().Add(HostingOIDCRefreshMargin)) {
		credential, err := t.source.credential(req.Context())
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		t.current = credential
	}
	token := t.current.Token
	t.mu.Unlock()
	copy := req.Clone(req.Context())
	copy.Header.Set("Authorization", "Bearer "+token)
	return t.next.RoundTrip(copy)
}
