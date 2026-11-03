package mcpmanagement

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/safetext"
)

type registrationRequest struct {
	ClientName    string   `json:"client_name"`
	RedirectURIs  []string `json:"redirect_uris"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
}

func (r *remoteServer) registerClient(w http.ResponseWriter, req *http.Request) {
	var input registrationRequest
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	decoder := json.NewDecoder(req.Body)
	if err != nil || mediaType != "application/json" || decoder.Decode(&input) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || !r.validRegistration(input) {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "Register allowed redirect URIs, authorization_code and refresh_token grants, code response type, and token authentication method none")
		return
	}
	id, err := randomToken("gmcp_client_")
	if err != nil {
		oauthError(w, 500, "server_error", "Could not register client")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	if len(r.clients) >= r.capacity() {
		oauthError(w, 429, "temporarily_unavailable", "Client registration capacity reached")
		return
	}
	if !r.registrationAllowedLocked(req) {
		oauthError(w, 429, "temporarily_unavailable", "Too many client registrations from this address; try again later")
		return
	}
	r.clients[id] = &oauthClient{ID: id, Name: safetext.Truncate(safetext.Clean(input.ClientName), 80), Redirects: append([]string(nil), input.RedirectURIs...), Expires: time.Now().Add(24 * time.Hour)}
	writeOAuthJSON(w, http.StatusCreated, map[string]any{"client_id": id, "client_id_issued_at": time.Now().Unix(), "redirect_uris": input.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
}

func (r *remoteServer) validRegistration(input registrationRequest) bool {
	if len(input.RedirectURIs) == 0 || len(input.RedirectURIs) > 8 || len(input.ClientName) > 256 || (input.AuthMethod != "" && input.AuthMethod != "none") {
		return false
	}
	for _, redirect := range input.RedirectURIs {
		if !r.redirectAllowed(redirect) {
			return false
		}
	}
	for _, grant := range input.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return false
		}
	}
	for _, response := range input.ResponseTypes {
		if response != "code" {
			return false
		}
	}
	return true
}

var pkceChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var pkceVerifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (r *remoteServer) requestedScopes(raw string) ([]string, bool) {
	if raw == "" {
		return []string{scopeRead}, true
	}
	requested := strings.Fields(raw)
	if len(requested) == 0 || len(requested) > 2 || !slices.Contains(requested, scopeRead) {
		return nil, false
	}
	for _, scope := range requested {
		if !slices.Contains(r.scopes(), scope) {
			return nil, false
		}
	}
	slices.Sort(requested)
	return slices.Compact(requested), true
}

func (r *remoteServer) token(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "Invalid token request")
		return
	}
	// RFC 8707 permits refresh to inherit the grant's original resource.
	// No refresh can select a different resource or expand the original scope.
	resource := req.PostForm.Get("resource")
	if resource != r.opts.ResourceURL && (resource != "" || req.PostForm.Get("grant_type") != "refresh_token") {
		oauthError(w, 400, "invalid_target", "resource must identify this MCP endpoint")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	switch req.PostForm.Get("grant_type") {
	case "authorization_code":
		r.exchangeCodeLocked(w, req)
	case "refresh_token":
		r.refreshLocked(w, req)
	default:
		oauthError(w, 400, "unsupported_grant_type", "Only authorization_code and refresh_token are supported")
	}
}

func (r *remoteServer) exchangeCodeLocked(w http.ResponseWriter, req *http.Request) {
	form := req.PostForm
	key := tokenHash(form.Get("code"))
	code := r.codes[key]
	if code == nil || time.Now().After(code.Expires) || code.ClientID != form.Get("client_id") || code.Redirect != form.Get("redirect_uri") || !pkceVerifier.MatchString(form.Get("code_verifier")) {
		oauthError(w, 400, "invalid_grant", "Invalid authorization code, client, redirect URI, or PKCE verifier")
		return
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != code.Challenge {
		oauthError(w, 400, "invalid_grant", "Invalid PKCE verifier")
		return
	}
	delete(r.codes, key) // Consume only after all bindings pass; never reusable.
	g := r.grants[code.GrantID]
	if g == nil || g.revoked || time.Now().After(g.expires) {
		oauthError(w, 400, "invalid_grant", "Authorization grant expired")
		return
	}
	out, err := r.mintLocked(g)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not issue tokens")
		return
	}
	r.audit("mcp.token.issued", g)
	writeOAuthJSON(w, http.StatusOK, out)
}

func (r *remoteServer) refreshLocked(w http.ResponseWriter, req *http.Request) {
	form := req.PostForm
	key := tokenHash(form.Get("refresh_token"))
	if old := r.usedRefresh[key]; old != nil && old.clientID == form.Get("client_id") {
		r.revokeLocked(old)
		oauthError(w, 400, "invalid_grant", "Refresh token reuse revoked this authorization grant")
		return
	}
	g := r.refresh[key]
	if g == nil || g.revoked || time.Now().After(g.expires) || g.clientID != form.Get("client_id") || (form.Get("scope") != "" && form.Get("scope") != strings.Join(g.scopes, " ")) {
		oauthError(w, 400, "invalid_grant", "Invalid refresh token, client, or scope")
		return
	}
	if len(r.usedRefresh) >= maxRefreshHistory {
		oauthError(w, 429, "temporarily_unavailable", "Refresh capacity reached; reconnect later")
		return
	}
	out, err := r.mintLocked(g)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not rotate tokens")
		return
	}
	r.usedRefresh[key] = g
	r.audit("mcp.token.refreshed", g)
	writeOAuthJSON(w, http.StatusOK, out)
}

func (r *remoteServer) revoke(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "Invalid revocation request")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	key := tokenHash(req.PostForm.Get("token"))
	g := r.access[key]
	if g == nil {
		g = r.refresh[key]
	}
	if g == nil {
		g = r.usedRefresh[key]
	}
	if g != nil && g.clientID == req.PostForm.Get("client_id") {
		r.revokeLocked(g)
	}
	w.WriteHeader(http.StatusOK)
}
