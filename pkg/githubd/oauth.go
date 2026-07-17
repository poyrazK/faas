// OAuth + install-token plumbing (slice 8, ADR-012).
//
// githubd is the only daemon that talks to api.github.com; this
// file owns the two outbound calls the M7.5 dashboard needs:
//
//   - ExchangeInstallationToken: turn an installation ID + a fresh
//     GitHub-App JWT into a per-installation access token (used for
//     every repo-scoped call: check-runs, content reads, etc.)
//   - ListInstallableRepos: enumerate the repos the installation
//     can see (used by the dashboard's repo-picker).
//
// The HTTP layer is intentionally minimal — only the request shapes
// the OAuth flow actually uses land here. The full GitHub REST
// surface is post-M7.5 work.
//
// Auth model: the GitHub App private key never leaves this package;
// it's read once at boot from /etc/faas/secrets/github-app.pem and
// cached in memory. The token cache (tokencache.go) is the consumer
// of ExchangeInstallationToken.
package githubd

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHubAPI is the base URL for api.github.com. Tests override via
// NewClient to point at an httptest.Server.
const GitHubAPI = "https://api.github.com"

// HTTPClient is the minimum interface githubd needs from net/http.
// net/http.Client satisfies it; tests inject a stub.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// JWT minting. We use github.com/golang-jwt/jwt for the RS256
// signing path; that dep is added by this slice.

// AppAuth holds the GitHub App credentials loaded at boot. The
// private key never escapes this struct — it's only used by
// MintAppJWT (which itself feeds the OAuth flow, not the public
// surface).
type AppAuth struct {
	AppID      string // GitHub App ID (numeric, as a string)
	PrivateKey *rsa.PrivateKey
	HTTPClient HTTPClient
}

// NewAppAuth loads and validates the GitHub App credentials.
// Returns an error if the key can't be parsed — the daemon must
// not start with a half-configured install.
func NewAppAuth(appID string, keyPEM []byte, hc HTTPClient) (*AppAuth, error) {
	if appID == "" {
		return nil, fmt.Errorf("githubd: app id required")
	}
	if len(keyPEM) == 0 {
		return nil, fmt.Errorf("githubd: app private key required")
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	key, err := parseRSAPrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("githubd: parse app key: %w", err)
	}
	return &AppAuth{AppID: appID, PrivateKey: key, HTTPClient: hc}, nil
}

// MintAppJWT produces a 10-minute RS256 JWT signed by the GitHub
// App private key. Per GitHub's docs, JWTs are valid for at most
// 15 minutes; we use 10 to leave a safety margin against clock
// drift between us and api.github.com.
func (a *AppAuth) MintAppJWT() (string, error) {
	if a == nil || a.PrivateKey == nil {
		return "", fmt.Errorf("githubd: app auth not initialized")
	}
	now := time.Now()
	tok, err := jwtSignRS256(
		a.AppID,
		a.PrivateKey,
		now.Add(-30*time.Second), // iat skew tolerance
		now.Add(10*time.Minute),
	)
	if err != nil {
		return "", fmt.Errorf("githubd: sign app jwt: %w", err)
	}
	return tok, nil
}

// ExchangeInstallationToken turns an installation ID + a freshly
// minted App JWT into a per-installation access token. The token
// is cached in tokencache.go so this call only happens on cache
// miss / expiry.
//
// Endpoint: POST https://api.github.com/app/installations/{id}/access_tokens
// Auth: Bearer <app JWT>
// Response: { "token": "...", "expires_at": "2024-01-01T00:00:00Z" }
func (a *AppAuth) ExchangeInstallationToken(ctx context.Context, installationID int64) (string, time.Time, error) {
	jwt, err := a.MintAppJWT()
	if err != nil {
		return "", time.Time{}, err
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", GitHubAPI, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubd: exchange install token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return "", time.Time{}, fmt.Errorf("githubd: exchange install token: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("githubd: decode install token: %w", err)
	}
	if payload.Token == "" {
		return "", time.Time{}, fmt.Errorf("githubd: install token response missing token field")
	}
	return payload.Token, payload.ExpiresAt, nil
}

// InstallableRepo is one entry in the list returned by
// ListInstallableRepos. Only the fields the dashboard's repo-picker
// UI needs are decoded.
type InstallableRepo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// ListInstallableRepos enumerates the repos the installation has
// access to. GitHub paginates at 100 per page; we walk pages until
// the Link header says we're done (or until pageCount cap, whichever
// comes first — a defensive cap against a misconfigured install
// that points at a 100k-repo org).
//
// Endpoint: GET https://api.github.com/installation/repositories
// Auth: Bearer <installation token>
func (a *AppAuth) ListInstallableRepos(ctx context.Context, installToken string, pageCountCap int) ([]InstallableRepo, error) {
	if pageCountCap <= 0 {
		pageCountCap = 20 // 20 pages × 100 = 2000 repos; covers any reasonable v1.0 install
	}
	var repos []InstallableRepo
	nextURL := fmt.Sprintf("%s/installation/repositories?per_page=100", GitHubAPI)
	for page := 0; page < pageCountCap && nextURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+installToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "faas-githubd/1.0")

		resp, err := a.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("githubd: list repos page %d: %w", page, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		// Capture the Link header BEFORE Close — some transports
		// strip headers on close (not httptest.Server.Client,
		// but defensive against future rewrites).
		linkHdr := resp.Header.Get("Link")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("githubd: list repos page %d: status=%d body=%s", page, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var payload struct {
			Repositories []InstallableRepo `json:"repositories"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("githubd: decode repos page %d: %w", page+1, err)
		}
		repos = append(repos, payload.Repositories...)
		nextURL = nextLink(linkHdr)
	}
	return repos, nil
}

// nextLink parses GitHub's Link header and returns the URL of the
// next page (the entry with rel="next"). Empty string = no more pages.
func nextLink(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		segment := strings.TrimSpace(part)
		if !strings.Contains(segment, `rel="next"`) {
			continue
		}
		lt := strings.Index(segment, "<")
		gt := strings.Index(segment, ">")
		if lt < 0 || gt < 0 || gt <= lt {
			continue
		}
		u, err := url.Parse(segment[lt+1 : gt])
		if err != nil {
			return ""
		}
		return u.String()
	}
	// Some clients/proxies split into multiple Link headers,
	// each with one entry. The Go http.Header.Get joins them with
	// ", " so this branch should never fire — but if it does,
	// the caller gets empty string and pagination stops, which
	// is the safer default.
	return ""
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key (PKCS#1
// or PKCS#8). Both shapes are accepted because GitHub's docs are
// ambiguous about which one App installers download.
func parseRSAPrivateKey(pem []byte) (*rsa.PrivateKey, error) {
	return parseRSAPrivateKeyPEM(pem)
}

// MarshalJSONForTest is a test-only helper that round-trips a
// struct through json so tests can assert on the wire shape.
func MarshalJSONForTest(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
