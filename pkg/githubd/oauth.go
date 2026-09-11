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
//
// ClientID + ClientSecret are the GitHub App's OAuth credentials
// (added by PR-C), distinct from the App private key: the App
// private key signs installation JWTs (server-to-server auth) and
// the ClientID/ClientSecret pair authenticates the user-to-server
// OAuth flow that ExchangeOAuthCode drives. Empty values mean the
// user-to-server methods (ExchangeUserOAuthCode,
// ListInstallationsForUser) return an error so a half-configured
// box can't accidentally bypass the §11 ownership proof.
type AppAuth struct {
	AppID        string // GitHub App ID (numeric, as a string)
	ClientID     string // GitHub App OAuth Client ID (PR-C)
	ClientSecret string // GitHub App OAuth Client Secret (PR-C)
	PrivateKey   *rsa.PrivateKey
	HTTPClient   HTTPClient
}

// NewAppAuth loads and validates the GitHub App credentials.
// Returns an error if the key can't be parsed — the daemon must
// not start with a half-configured install.
//
// clientID + clientSecret are the OAuth credentials PR-C's
// user-to-server flow needs; pass empty strings to disable that
// flow (the install-token flow still works).
func NewAppAuth(appID string, keyPEM []byte, hc HTTPClient, clientID, clientSecret string) (*AppAuth, error) {
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
	return &AppAuth{
		AppID:        appID,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		PrivateKey:   key,
		HTTPClient:   hc,
	}, nil
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

// GitHubOAuthAccessTokenURL is the user-to-server OAuth token
// exchange endpoint. Distinct from api.github.com: the user OAuth
// flow hits login/oauth/access_token with form-encoded
// client_id+client_secret+code, not with the App JWT.
const GitHubOAuthAccessTokenURL = "https://github.com/login/oauth/access_token"

// ExchangeUserOAuthCode trades a one-shot `code` for a user-to-server
// access token (PR-C). The apid OAuth-code-callback handler hands us
// the `code` that arrived in the dashboard's
// ?code=…&state=… redirect; this method trades it for the user
// access token that authorizes /user/installations.
//
// Returns the access_token string. On a non-200 response, decodes
// the response body's `error` field and returns it wrapped so the
// caller can distinguish bad-verification-code (400) from network
// failure (transport error). Returns an error when AppAuth is not
// configured with ClientID+ClientSecret (defense-in-depth: a box
// that didn't ship the OAuth credentials can't accidentally bypass
// the §11 ownership proof).
func (a *AppAuth) ExchangeUserOAuthCode(ctx context.Context, code string) (string, error) {
	if a == nil || a.ClientID == "" || a.ClientSecret == "" {
		return "", fmt.Errorf("githubd: user-to-server oauth not configured (ClientID/ClientSecret missing)")
	}
	if code == "" {
		return "", fmt.Errorf("githubd: code required")
	}
	form := "client_id=" + url.QueryEscape(a.ClientID) +
		"&client_secret=" + url.QueryEscape(a.ClientSecret) +
		"&code=" + url.QueryEscape(code)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GitHubOAuthAccessTokenURL, strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "faas-githubd/1.0")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("githubd: exchange user oauth code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("githubd: exchange user oauth code: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("githubd: decode user oauth code: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("githubd: user oauth code rejected: %s (%s)", payload.Error, payload.ErrorDesc)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("githubd: user oauth response missing access_token field")
	}
	return payload.AccessToken, nil
}

// UserInstallation is one entry from /user/installations. We only
// need the id (to mint the install token via
// ExchangeInstallationToken) and the account.login (to seal against
// §11 mismatches on the durable row).
type UserInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// ListInstallationsForUser enumerates the GitHub App installs visible
// to a user via their user-to-server access token. PR-C's
// ExchangeOAuthCode flow calls this with the access_token returned
// by ExchangeUserOAuthCode. The first install is the one we'll
// bind (PR-C's single-install-per-account assumption); the account
// login is captured for the §11 AuditGithubLogin paper trail on
// the durable github_installations row.
//
// Endpoint: GET https://api.github.com/user/installations?per_page=100
// Auth:    Bearer <user-token>
// Response: { "installations": [{ "id": ..., "account": { "login": ... } }, ...] }
func (a *AppAuth) ListInstallationsForUser(ctx context.Context, userToken string) ([]UserInstallation, error) {
	if userToken == "" {
		return nil, fmt.Errorf("githubd: user token required")
	}
	endpoint := GitHubAPI + "/user/installations?per_page=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubd: list user installations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("githubd: list user installations: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Installations []UserInstallation `json:"installations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("githubd: decode user installations: %w", err)
	}
	return payload.Installations, nil
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

// Installation is the decoded body of GET /app/installations/{id}.
// githubd uses it to confirm a callback's installation_id is real
// for the configured GitHub App before persisting a binding
// (review finding #1+#2 closure for the M7.5 OAuth path).
//
// Endpoint: GET https://api.github.com/app/installations/{id}
// Auth:    Bearer <app JWT>
// Status:  200 → real install, 404 → forged/unknown id, 401/403 →
//
//	app JWT rejected (revoked key, wrong app).
type Installation struct {
	ID           int64  `json:"id"`
	AccountLogin string `json:"account_login"` // nested in account.login; we keep a flat copy for the proto
	Account      struct {
		Login string `json:"login"`
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"` // "all" | "selected"
}

// VerifyInstallation confirms an installation_id returned by a GitHub
// App install callback actually exists for the configured GitHub
// App. Returns verified=true + the install's default branch on
// success; verified=false on a 404 (forged/unknown id). Transport
// errors are returned as a non-nil err so the caller can distinguish
// "no install" (verified=false, err=nil) from "couldn't reach
// GitHub" (verified=false, err=non-nil). The dashboard should
// refuse to persist a binding in either case.
//
// Note: the per-installation /access_tokens POST already proves the
// install exists when the dashboard later calls ExchangeInstallationToken;
// this method is the dedicated "trust on first contact" check that
// closes the §11 least-privilege regression where the M7.5 PR shipped
// without one.
//
// expectedLogin is the §11 ownership proof (PR-B). When non-empty,
// the install's account.login MUST match expectedLogin for
// verified=true; on mismatch the function returns (zero, false, nil)
// so the caller can render a clean 403 without leaking "does this
// install exist" to a forged caller. Empty expectedLogin preserves
// the pre-PR-B wire shape.
func (a *AppAuth) VerifyInstallation(ctx context.Context, installationID int64, expectedLogin string) (Installation, bool, error) {
	if a == nil || a.PrivateKey == nil {
		return Installation{}, false, fmt.Errorf("githubd: app auth not initialized")
	}
	if installationID <= 0 {
		return Installation{}, false, fmt.Errorf("githubd: invalid installation id %d", installationID)
	}
	jwt, err := a.MintAppJWT()
	if err != nil {
		return Installation{}, false, err
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d", GitHubAPI, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Installation{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return Installation{}, false, fmt.Errorf("githubd: verify install: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		// Forged or stale id — GitHub doesn't know this install.
		return Installation{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return Installation{}, false, fmt.Errorf("githubd: verify install: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload Installation
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Installation{}, false, fmt.Errorf("githubd: decode install: %w", err)
	}
	payload.AccountLogin = payload.Account.Login
	// §11 ownership check: the install must belong to the user the
	// dashboard session claims. Mismatch is a forged callback trying
	// to adopt someone else's installation.
	if expectedLogin != "" && payload.Account.Login != expectedLogin {
		return Installation{}, false, nil
	}
	return payload, true, nil
}

// ListInstallableRepos enumerates the repos the installation has
// access to. GitHub paginates at 100 per page; we walk pages until
// the Link header says we're done. A defensive page cap protects the
// process from a misconfigured install that points at a 100k-repo org;
// reaching that cap is an error so callers never mistake a truncated list
// for the complete access set and detach valid bindings.
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
	if nextURL != "" {
		return nil, fmt.Errorf("githubd: list repos page cap %d reached before pagination completed", pageCountCap)
	}
	return repos, nil
}

// nextLink parses GitHub's Link header and returns the URL of the
// next page (the entry with rel="next"). Empty string = no more pages.
//
// RFC 8288 §3 grammar (simplified):
//
//	Link        = #link-value
//	link-value  = "<" URI-Reference ">" *( ";" link-param )
//	link-param  = token BWS "=" BWS ( token / quoted-string )
//
// Each entry is comma-separated. The URL is the <…> field; the rest
// of the segment is semicolon-separated key=value params. We split
// on ';' and look for a param whose key (lower-cased) is `rel` and
// value (lower-cased, with surrounding quotes stripped) is `next`.
//
// Review finding #9: the previous implementation used strings.Index
// to find the first '<' and '>' on the segment, which truncates
// early when the URL field contains its own '>' — for example
// <https://api.github.com/repos?page=2&q=>foo&per_page=100>. The
// param-aware split fixes this: the URL ends at the '>' that
// precedes the first ';' (or the closing '>' on the whole segment
// if no params follow).
func nextLink(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		segment := strings.TrimSpace(part)
		uri, ok := extractLinkURI(segment)
		if !ok {
			continue
		}
		if linkHasRel(segment, "next") {
			return uri
		}
	}
	// Some clients/proxies split into multiple Link headers,
	// each with one entry. The Go http.Header.Get joins them with
	// ", " so this branch should never fire — but if it does,
	// the caller gets empty string and pagination stops, which
	// is the safer default.
	return ""
}

// extractLinkURI returns the <URI-Reference> field of a single
// link-value segment (the substring between the leading '<' and
// the '>' that closes it). Returns false if the segment doesn't
// start with '<' or has no closing '>'.
//
// The closing '>' is whichever comes first: the '>' that terminates
// the URI-reference (RFC 8288 §3.3 URI-Reference disallows '>' inside
// unreserved+reserved chars used in practice), or — defensively —
// the first '>' that precedes a ';' (start of the first link-param).
// In practice GitHub's pagination URLs never contain '>', so the
// simple "first '>'" rule is correct; the ';' guard handles the
// hypothetical future case where a vendor encodes a '>' inside a
// quoted-string param value.
func extractLinkURI(segment string) (string, bool) {
	if !strings.HasPrefix(segment, "<") {
		return "", false
	}
	rest := segment[1:]
	// Split on ';' to isolate the URI-reference from any
	// link-params that follow. The first ';' is the canonical
	// end of the URI field.
	if semi := strings.Index(rest, ";"); semi >= 0 {
		uriPart := rest[:semi]
		if gt := strings.LastIndex(uriPart, ">"); gt >= 0 {
			return uriPart[:gt], true
		}
		return "", false
	}
	gt := strings.Index(rest, ">")
	if gt < 0 {
		return "", false
	}
	return rest[:gt], true
}

// linkHasRel reports whether the segment (a single link-value) has a
// link-param token equal to rel=<value>. The token comparison is
// case-insensitive on both key and value (RFC 8288 §3.3 says link
// parameter names are case-insensitive; the value is compared as a
// quoted-string, so we strip surrounding quotes before comparing).
//
// Both single and double quotes are accepted as quoted-string
// delimiters — RFC 8288 only mandates double quotes, but Go's
// net/http Link header serialization is permissive (it round-trips
// whatever the server sent), and we want a single test contract.
func linkHasRel(segment, want string) bool {
	parts := strings.Split(segment, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(p[:eq]))
		val := strings.TrimSpace(p[eq+1:])
		// Strip a matching pair of surrounding quotes (single OR
		// double); mismatched quotes (e.g. `rel='next"`) are left
		// alone — that wouldn't be a valid RFC 8288 segment and
		// would fail to match anyway.
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "rel" && strings.EqualFold(val, want) {
			return true
		}
	}
	return false
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
