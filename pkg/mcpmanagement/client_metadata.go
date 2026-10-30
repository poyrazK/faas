package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/publicdest"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Client ID Metadata Documents (ADR-20261030065746, MCP 2026-07-28): a client
// whose client_id is an HTTPS URL publishes its metadata there, and the server
// fetches it instead of the client registering. The document proves which
// site the client is; the operator's redirect allowlist still decides where it
// may send codes.
const (
	clientMetadataMaxBytes   = 5 << 10
	clientMetadataTimeout    = 5 * time.Second
	clientMetadataMinTTL     = 5 * time.Minute
	clientMetadataDefaultTTL = time.Hour
	clientMetadataMaxTTL     = 24 * time.Hour
	clientMetadataURLMax     = 2048
	clientMetadataRedirects  = 16
)

var errClientMetadata = errors.New("client metadata document refused")

type clientMetadataDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// isClientMetadataURL reports whether a client_id names a metadata document:
// an https URL with a path, no credentials, fragment, or dot segments.
func isClientMetadataURL(id string) bool {
	if len(id) > clientMetadataURLMax || !strings.HasPrefix(id, "https://") {
		return false
	}
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.Path == "" || u.Path == "/" {
		return false
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// newClientMetadataHTTPClient fetches documents from public addresses only,
// checked after DNS at every connection, without following redirects.
func newClientMetadataHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext:            publicdest.NewDialer().DialContext,
		TLSHandshakeTimeout:    clientMetadataTimeout,
		ResponseHeaderTimeout:  clientMetadataTimeout,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
	}
	return &http.Client{Transport: transport, Timeout: clientMetadataTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// fetchClientMetadata loads and checks a client's document and how long it
// may be cached.
func (r *remoteServer) fetchClientMetadata(ctx context.Context, id string) (clientMetadataDocument, time.Duration, error) {
	var doc clientMetadataDocument
	ctx, cancel := context.WithTimeout(ctx, clientMetadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return doc, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.metadataClient.Do(req)
	if err != nil {
		return doc, 0, fmt.Errorf("fetch client metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doc, 0, fmt.Errorf("%w: document answered %d", errClientMetadata, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, clientMetadataMaxBytes+1))
	if err != nil {
		return doc, 0, fmt.Errorf("read client metadata: %w", err)
	}
	if len(body) > clientMetadataMaxBytes || json.Unmarshal(body, &doc) != nil {
		return doc, 0, fmt.Errorf("%w: document is not a JSON object within %d bytes", errClientMetadata, clientMetadataMaxBytes)
	}
	if err := validClientMetadata(id, doc); err != nil {
		return doc, 0, err
	}
	return doc, clientMetadataTTL(resp.Header.Get("Cache-Control")), nil
}

func validClientMetadata(id string, doc clientMetadataDocument) error {
	switch {
	case doc.ClientID != id:
		return fmt.Errorf("%w: client_id does not match the document URL", errClientMetadata)
	case len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > clientMetadataRedirects:
		return fmt.Errorf("%w: redirect_uris must list 1 to %d URIs", errClientMetadata, clientMetadataRedirects)
	case len(doc.ClientName) > 256:
		return fmt.Errorf("%w: client_name is too long", errClientMetadata)
	case doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none":
		return fmt.Errorf("%w: only public clients (token_endpoint_auth_method none) are supported", errClientMetadata)
	}
	for _, grant := range doc.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return fmt.Errorf("%w: unsupported grant type", errClientMetadata)
		}
	}
	for _, response := range doc.ResponseTypes {
		if response != "code" {
			return fmt.Errorf("%w: unsupported response type", errClientMetadata)
		}
	}
	return nil
}

// clientMetadataTTL honors a document's max-age within the server's bounds.
func clientMetadataTTL(cacheControl string) time.Duration {
	ttl := clientMetadataDefaultTTL
	for _, directive := range strings.Split(cacheControl, ",") {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if directive == "no-store" || directive == "no-cache" {
			ttl = clientMetadataMinTTL
		}
		if value, ok := strings.CutPrefix(directive, "max-age="); ok {
			if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	}
	return min(max(ttl, clientMetadataMinTTL), clientMetadataMaxTTL)
}

// resolveMetadataClient registers the client a metadata-document client_id
// names, fetching its document unless a fresh copy is cached. Its redirect
// URIs are the document's that the operator allows. Other client IDs are left
// to ordinary registration.
func (r *remoteServer) resolveMetadataClient(req *http.Request, id string) error {
	if r.metadataClient == nil || !isClientMetadataURL(id) {
		return nil
	}
	r.mu.Lock()
	if c := r.clients[id]; c != nil && c.Metadata && time.Now().Before(c.Refresh) {
		r.mu.Unlock()
		return nil
	}
	// A fetch stands in for a registration, so it shares that budget.
	if !r.registrationAllowedLocked(req) {
		r.mu.Unlock()
		return fmt.Errorf("%w: too many client metadata fetches from this address", errClientMetadata)
	}
	r.mu.Unlock()
	doc, ttl, err := r.fetchClientMetadata(req.Context(), id)
	if err != nil {
		return err
	}
	var redirects []string
	for _, redirect := range doc.RedirectURIs {
		if r.redirectAllowed(redirect) {
			redirects = append(redirects, redirect)
		}
	}
	if len(redirects) == 0 {
		return fmt.Errorf("%w: none of the client's redirect URIs is allowed on this server", errClientMetadata)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	existing := r.clients[id]
	if existing == nil && len(r.clients) >= r.capacity() {
		return fmt.Errorf("%w: client capacity reached", errClientMetadata)
	}
	expires := now.Add(24 * time.Hour)
	if existing != nil && existing.Expires.After(expires) {
		expires = existing.Expires
	}
	r.clients[id] = &oauthClient{ID: id, Name: safetext.Truncate(safetext.Clean(doc.ClientName), 80), Redirects: redirects, Expires: expires, Metadata: true, Refresh: now.Add(ttl)}
	return nil
}

// clientHost is the site a metadata-document client proved it is.
func clientHost(c *oauthClient) string {
	if c == nil || !c.Metadata {
		return ""
	}
	u, err := url.Parse(c.ID)
	if err != nil {
		return ""
	}
	return u.Host
}
