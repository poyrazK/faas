package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RegistryClient is a minimal OCI/Docker registry v2 client. It resolves a
// (possibly tag-only) reference to its content-addressable digest via the
// manifest endpoint, performing the anonymous Bearer-token dance public
// registries (Docker Hub, ghcr.io) require. It implements Puller.
//
// Scope (M6 groundwork): reference → digest resolution. Layer/config blob
// streaming for the app-layer build lands in a follow-up. Egress hardening
// (deny RFC1918 / metadata ranges, spec §11) is applied by injecting a
// policy-aware *http.Client via WithHTTPClient — this type does not itself
// enforce it.
type RegistryClient struct {
	hc     *http.Client
	scheme string // "https" in production; the test seam sets "http"
	host   string // "" = derive from the reference; tests pin an httptest host
	ua     string
}

// compile-time assertion the client satisfies the puller seam imaged consumes.
var _ Puller = (*RegistryClient)(nil)

// Option configures a RegistryClient.
type Option func(*RegistryClient)

// WithHTTPClient injects the HTTP client (timeouts, egress-policy transport).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *RegistryClient) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// WithEndpoint pins the scheme and API host for every request, bypassing the
// per-reference host derivation. Used by tests to point at an httptest server;
// not for production use.
func WithEndpoint(scheme, host string) Option {
	return func(c *RegistryClient) {
		c.scheme = scheme
		c.host = host
	}
}

// NewRegistryClient builds a client with sensible defaults (HTTPS, 30 s timeout).
func NewRegistryClient(opts ...Option) *RegistryClient {
	c := &RegistryClient{
		hc:     &http.Client{Timeout: 30 * time.Second},
		scheme: "https",
		ua:     "faas-imaged/1 (+https://DOMAIN)",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// manifestAccept lists the manifest media types we can resolve a digest from.
var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}, ", ")

// PullDigest resolves ref to its canonical digest. A digest-pinned reference is
// confirmed to exist; a tag reference is resolved to the digest the registry
// currently serves. Satisfies Puller.
func (c *RegistryClient) PullDigest(ctx context.Context, ref string) (string, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return "", err
	}
	return c.resolveDigest(ctx, r)
}

func (c *RegistryClient) baseURL(r Reference) string {
	host := c.host
	if host == "" {
		host = r.APIHost()
	}
	return c.scheme + "://" + host
}

func (c *RegistryClient) resolveDigest(ctx context.Context, r Reference) (string, error) {
	url := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()

	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return "", err
	}
	// A 401 carries the token challenge: fetch a bearer token and retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch)
		if err != nil {
			return "", err
		}
		if resp, err = c.getManifest(ctx, url, token); err != nil {
			return "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("oci: manifest %s: registry returned %d: %s",
			r.String(), resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Prefer the registry's content digest header; fall back to hashing the
	// manifest body ourselves (some registries omit it).
	if dg := resp.Header.Get("Docker-Content-Digest"); dg != "" {
		if err := validateDigest(dg); err != nil {
			return "", fmt.Errorf("oci: %s returned malformed Docker-Content-Digest: %w", r.String(), err)
		}
		return dg, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // manifests are tiny; cap at 8 MiB
	if err != nil {
		return "", fmt.Errorf("oci: read manifest %s: %w", r.String(), err)
	}
	sum := sha256.Sum256(body)
	return digestAlgo + hex.EncodeToString(sum[:]), nil
}

func (c *RegistryClient) getManifest(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oci: build manifest request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	req.Header.Set("User-Agent", c.ua)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch manifest: %w", err)
	}
	return resp, nil
}

// fetchToken performs the anonymous Bearer-token GET the WWW-Authenticate
// challenge points at (realm?service=&scope=). Public pulls need no credentials.
func (c *RegistryClient) fetchToken(ctx context.Context, ch authChallenge) (string, error) {
	if ch.realm == "" {
		return "", fmt.Errorf("oci: 401 with no bearer realm; not a public registry?")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ch.realm, nil)
	if err != nil {
		return "", fmt.Errorf("oci: build token request: %w", err)
	}
	q := req.URL.Query()
	if ch.service != "" {
		q.Set("service", ch.service)
	}
	if ch.scope != "" {
		q.Set("scope", ch.scope)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", c.ua)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oci: fetch token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oci: token endpoint returned %d", resp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return "", fmt.Errorf("oci: decode token: %w", err)
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	if tok.AccessToken != "" {
		return tok.AccessToken, nil
	}
	return "", fmt.Errorf("oci: token endpoint returned no token")
}

// authChallenge is the parsed subset of a `WWW-Authenticate: Bearer …` header.
type authChallenge struct {
	realm   string
	service string
	scope   string
}

// parseChallenge extracts realm/service/scope from a Bearer challenge header
// (e.g. `Bearer realm="https://auth/token",service="registry",scope="repository:x:pull"`).
func parseChallenge(header string) authChallenge {
	var ch authChallenge
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return ch
	}
	for _, part := range splitParams(rest) {
		k, v, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "realm":
			ch.realm = v
		case "service":
			ch.service = v
		case "scope":
			ch.scope = v
		}
	}
	return ch
}

// splitParams splits a challenge's comma-separated key="value" params without
// breaking on commas inside quoted values (scopes can contain them).
func splitParams(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}
