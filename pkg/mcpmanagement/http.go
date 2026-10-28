package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const scopeRead = "gregale:read"
const scopeWrite = "gregale:write"

// HTTPOptions configures a self-hosted OAuth resource and authorization server.
// ResourceURL is the public HTTPS /mcp URL, served behind a TLS reverse proxy.
// RedirectURIs is an operator-controlled allowlist, never client input. An
// HTTP loopback URI without a port allows the client's ephemeral port only.
// The loopback /callback/* rule additionally allows Codex's random final path
// component; the actual registered URI remains exact for authorization/exchange.
// No selected CLI credential is used by the remote server.
type HTTPOptions struct {
	Lifecycle    *Lifecycle
	Telemetry    *Telemetry
	APIBase      string
	ResourceURL  string
	RedirectURIs []string
	Version      string
	Timeout      time.Duration
	AllowWrites  bool
	// Toolsets limits the tools every grant lists; nil lists all.
	Toolsets []string
	// Onboarding is browser (default) or the explicit api-key fallback.
	Onboarding  string
	StateStore  *StateStore
	AuditLogger *slog.Logger
	// APIHTTPClient allows transport configuration, including private CA trust.
	APIHTTPClient *http.Client
	// UploadDir, on the hosted bridge, holds source uploads and the
	// directories plans deploy from. It is emptied on start.
	UploadDir string
	// CLIRunner, on the hosted bridge, runs the gregale CLI as a grant with
	// its own credential; provisioning tools use it.
	CLIRunner func(token string) LocalCLI
	// Hosted serves Gregale's own bridge at https://api.<platform>/mcp
	// (ADR-20261026195952). The issuer is the resource URL itself, so the
	// OAuth endpoints live under /mcp and the metadata at the RFC 8414 path
	// insertion URLs; the host's root metadata belongs to the API.
	Hosted bool
}

type remoteServer struct {
	opts   HTTPOptions
	origin string
	// issuer is the origin, or the resource URL on the hosted bridge; every
	// OAuth endpoint is issuer + "/oauth/...".
	issuer  string
	host    string
	uploads *uploadStore
	// registrations counts client registrations per address on the hosted
	// bridge, in fixed windows.
	registrations map[string]registrationWindow
	mu            sync.Mutex
	clients       map[string]*oauthClient
	requests      map[string]*authorizationRequest
	codes         map[string]*authorizationCode
	grants        map[string]*oauthGrant
	access        map[string]*oauthGrant
	refresh       map[string]*oauthGrant
	usedRefresh   map[string]*oauthGrant
}

type grantContextKey struct{}

// grantServerKey carries the connection's MCP server, held for the request.
type grantServerKey struct{}

// NewHTTPHandler exposes stateless Streamable HTTP and an OAuth 2.1 bridge.
// Each grant owns a separate SDK server/client/plan store and account credential.
// An optional encrypted state store preserves unexpired grants across restart.
func NewHTTPHandler(opts HTTPOptions) (http.Handler, error) {
	service, err := NewHTTPService(opts)
	if err != nil {
		return nil, err
	}
	return service.Handler(), nil
}

// RemoteService provides separate public OAuth/MCP and private operator handlers.
type RemoteService struct {
	public http.Handler
	remote *remoteServer
}

func (s *RemoteService) Handler() http.Handler { return s.public }

// NewHTTPService constructs the bridge and its independent local control handler.
func NewHTTPService(opts HTTPOptions) (*RemoteService, error) {
	u, err := url.Parse(opts.ResourceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" || u.RawPath != "" {
		return nil, errors.New("remote resource URL must be an HTTPS URL ending in /mcp without credentials, query, or fragment")
	}
	if len(opts.RedirectURIs) == 0 {
		return nil, errors.New("remote OAuth requires at least one exact allowed redirect URI")
	}
	for _, redirect := range opts.RedirectURIs {
		if !validConfiguredRedirectURI(redirect) {
			return nil, errors.New("OAuth redirect URIs must be HTTPS or loopback HTTP without credentials or fragments")
		}
	}
	apiURL, err := url.Parse(opts.APIBase)
	if err != nil || apiURL.Host == "" || apiURL.User != nil || apiURL.RawQuery != "" || apiURL.Fragment != "" || (apiURL.Scheme != "https" && (apiURL.Scheme != "http" || !loopbackHost(apiURL.Hostname()))) {
		return nil, errors.New("remote API endpoint must use HTTPS, or HTTP on loopback, without credentials, query, or fragment")
	}
	if opts.Onboarding == "" {
		opts.Onboarding = "browser"
	}
	if opts.Onboarding != "browser" && opts.Onboarding != "api-key" {
		return nil, errors.New("onboarding must be browser or api-key")
	}
	if opts.Timeout == 0 {
		opts.Timeout = api.DefaultClientTimeout
	}
	if opts.Timeout <= 0 || opts.Timeout > time.Minute {
		return nil, errors.New("tool timeout must be positive and at most 1m")
	}
	// Copy caller-owned configuration before using it in concurrent handlers.
	opts.RedirectURIs = append([]string(nil), opts.RedirectURIs...)
	if opts.Lifecycle == nil {
		opts.Lifecycle = NewLifecycle()
	}
	if opts.Hosted && opts.Onboarding != "browser" {
		return nil, errors.New("the hosted bridge uses browser onboarding")
	}
	origin := u.Scheme + "://" + u.Host
	issuer := origin
	if opts.Hosted {
		issuer = opts.ResourceURL
	}
	r := &remoteServer{opts: opts, origin: origin, issuer: issuer, host: u.Host, clients: map[string]*oauthClient{}, requests: map[string]*authorizationRequest{}, codes: map[string]*authorizationCode{}, grants: map[string]*oauthGrant{}, access: map[string]*oauthGrant{}, refresh: map[string]*oauthGrant{}, usedRefresh: map[string]*oauthGrant{}}
	if opts.Hosted && opts.UploadDir != "" {
		if r.uploads, err = newUploadStore(opts.UploadDir, opts.ResourceURL); err != nil {
			return nil, err
		}
	}
	if err := r.restoreOAuth(); err != nil {
		return nil, err
	}
	opts.Telemetry.attachConnections(r)
	return &RemoteService{public: opts.Lifecycle.wrapHTTP(r.durableHandler(r.handler())), remote: r}, nil
}

func loopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func validRedirectURI(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && !strings.Contains(value, "*") && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && loopbackHost(u.Hostname())))
}

// Codex randomizes its final loopback callback path component. An operator can
// allow that single component with /callback/*; HTTPS wildcards, arbitrary
// prefixes, nested paths, encoded separators and query wildcards stay forbidden.
func validConfiguredRedirectURI(value string) bool {
	if validRedirectURI(value) {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "http" && loopbackHost(u.Hostname()) && u.User == nil && u.Fragment == "" && u.RawQuery == "" && u.Path == "/callback/*" && strings.HasSuffix(value, "/callback/*") && strings.Count(value, "*") == 1
}

func callbackSuffixAllowed(path string) bool {
	if !strings.HasPrefix(path, "/callback/") {
		return false
	}
	suffix := strings.TrimPrefix(path, "/callback/")
	if len(suffix) == 0 || len(suffix) > 128 {
		return false
	}
	for _, char := range suffix {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func (r *remoteServer) redirectAllowed(value string) bool {
	if !validRedirectURI(value) {
		return false
	}
	u, _ := url.Parse(value)
	for _, allowed := range r.opts.RedirectURIs {
		if value == allowed {
			return true
		}
		a, _ := url.Parse(allowed)
		if a.Path == "/callback/*" && a.Scheme == "http" && u.Scheme == "http" && a.Hostname() == u.Hostname() && (a.Port() == "" || a.Port() == u.Port()) && u.RawPath == "" && u.RawQuery == "" && callbackSuffixAllowed(u.Path) {
			return true
		}
		if a.Scheme == "http" && u.Scheme == "http" && loopbackHost(a.Hostname()) && a.Hostname() == u.Hostname() && a.Port() == "" && a.EscapedPath() == u.EscapedPath() && a.RawQuery == u.RawQuery {
			return true
		}
	}
	return false
}

func (r *remoteServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", r.resourceMetadata)
	prefix := ""
	if r.opts.Hosted {
		prefix = "/mcp"
		mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", r.authorizationMetadata)
		mux.HandleFunc("GET /.well-known/openid-configuration/mcp", r.authorizationMetadata)
	} else {
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", r.resourceMetadata)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", r.authorizationMetadata)
	}
	mux.HandleFunc("POST "+prefix+"/oauth/register", r.registerClient)
	mux.HandleFunc("GET "+prefix+"/oauth/authorize", r.authorize)
	mux.HandleFunc("POST "+prefix+"/oauth/consent", r.consent)
	mux.HandleFunc("POST "+prefix+"/oauth/token", r.token)
	mux.HandleFunc("POST "+prefix+"/oauth/revoke", r.revoke)
	mux.HandleFunc("GET "+prefix+"/oauth/continue", r.continueLogin)
	getServer := func(req *http.Request) *mcp.Server {
		server, _ := req.Context().Value(grantServerKey{}).(*mcp.Server)
		return server
	}
	mux.Handle("/mcp", r.authenticate(newWatchHTTPHandler(getServer)))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		setPrivateHeaders(w)
		if req.Host != r.host {
			http.Error(w, "Unrecognized host", http.StatusForbidden)
			return
		}
		if origin := req.Header.Get("Origin"); origin != "" && origin != r.origin {
			http.Error(w, "Origin is not allowed", http.StatusForbidden)
			return
		}
		if r.uploads != nil && strings.HasPrefix(req.URL.Path, "/mcp/uploads/") {
			// The capability URL authorizes the body; its limit is the plan's.
			r.uploads.receive(w, req)
			return
		}
		r.mu.Lock()
		r.pruneLocked()
		r.mu.Unlock()
		req.Body = http.MaxBytesReader(w, req.Body, MaxFrameBytes)
		mux.ServeHTTP(w, req)
	})
}

func setPrivateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

func writeOAuthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func (r *remoteServer) scopes() []string {
	if r.opts.AllowWrites {
		return []string{scopeRead, scopeWrite}
	}
	return []string{scopeRead}
}

func (r *remoteServer) resourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeOAuthJSON(w, http.StatusOK, map[string]any{"resource": r.opts.ResourceURL, "authorization_servers": []string{r.issuer}, "scopes_supported": r.scopes(), "bearer_methods_supported": []string{"header"}, "resource_name": "Gregale management"})
}

func (r *remoteServer) authorizationMetadata(w http.ResponseWriter, _ *http.Request) {
	writeOAuthJSON(w, http.StatusOK, map[string]any{"issuer": r.issuer, "authorization_endpoint": r.issuer + "/oauth/authorize", "token_endpoint": r.issuer + "/oauth/token", "registration_endpoint": r.issuer + "/oauth/register", "revocation_endpoint": r.issuer + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": r.scopes()})
}

func (r *remoteServer) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		parts := strings.Fields(req.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || req.URL.Query().Has("access_token") {
			r.challenge(w)
			return
		}
		key := tokenHash(parts[1])
		r.mu.Lock()
		g, leaseExpires, valid := r.accessLeaseLocked(key)
		r.mu.Unlock()
		if !valid {
			r.challenge(w)
			return
		}
		leaseCtx, stopLease := context.WithDeadline(req.Context(), leaseExpires)
		defer stopLease()
		verifyCtx, stopVerify := context.WithTimeout(leaseCtx, r.opts.Timeout)
		account, err := g.client.Whoami(verifyCtx)
		stopVerify()
		if err != nil || account.ID != g.accountID || leaseCtx.Err() != nil {
			r.challenge(w)
			return
		}
		server, currentExpiry, release, err := r.holdServer(key, g)
		if errors.Is(err, errStaleAccess) {
			r.challenge(w)
			return
		}
		if err != nil {
			oauthError(w, http.StatusInternalServerError, "server_error", "Could not prepare account tools")
			return
		}
		defer release()
		r.slideCredential(g)
		// Reverification may shorten a lease, but refresh cannot extend this request.
		if currentExpiry.Before(leaseExpires) {
			leaseExpires = currentExpiry
		}
		requestCtx, stopRequest := context.WithDeadline(leaseCtx, leaseExpires)
		defer stopRequest()
		requestCtx = context.WithValue(requestCtx, authorizationLeaseExpiresKey{}, leaseExpires)
		requestCtx = context.WithValue(requestCtx, grantContextKey{}, g)
		next.ServeHTTP(w, req.WithContext(context.WithValue(requestCtx, grantServerKey{}, server)))
	})
}

func (r *remoteServer) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+r.origin+`/.well-known/oauth-protected-resource/mcp", scope="`+scopeRead+`"`)
	oauthError(w, http.StatusUnauthorized, "invalid_token", "A valid resource-bound OAuth access token is required")
}

func (r *remoteServer) apiClient(key string) *api.Client {
	c := api.NewClient(r.opts.APIBase, key)
	c.SetCompletionCache(nil)
	if r.opts.APIHTTPClient != nil {
		*c.HTTPClient() = *r.opts.APIHTTPClient
	}
	c.HTTPClient().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
