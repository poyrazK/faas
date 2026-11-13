// Package sandboxpreview serves sandbox preview hosts (ADR-20261027090013
// §11): https://<port>-<sandbox id>.<preview domain> reaches the exposed
// guest port of a live session. gatewayd-internal serves it; the session
// node's vmmd connects to the guest port through guest-init over vsock
// (sandboxproto port_connect), so no guest port is ever reachable on the
// host's network.
//
// A preview host's route, its session's placement, plan and exposed ports,
// is read once and cached for RouteCacheTTL, misses included, so requests
// do not read Postgres. A private port answers only requests carrying its
// preview token (bearer, or the host-only cookie a browser gets by opening
// the URL with ?gregale_preview_token=); a public port answers anyone.
// Unknown hosts, ports that are not exposed and requests without a valid
// token answer 404, like an absent route.
package sandboxpreview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// RouteCacheTTL is how long a route, or its absence, is cached. A port
// stops being served at most this long after it is removed.
const RouteCacheTTL = 5 * time.Second

// CookieName is the host-only cookie a private preview's token rides in.
const CookieName = "__Host-gregale_preview"

// Route is what serving a session's preview hosts needs.
type Route struct {
	AccountID  string
	Plan       api.Plan
	Live       bool
	NodeID     string
	InstanceID string
	// Ports maps each exposed port to its visibility.
	Ports map[int]string
}

// ErrNoRoute is a session that does not exist.
var ErrNoRoute = errors.New("sandboxpreview: no such session")

// Routes reads a session's route; ErrNoRoute when there is none.
type Routes interface {
	PreviewRoute(ctx context.Context, sandboxID string) (Route, error)
}

// Tokens verifies preview tokens; *sandboxtoken.Verifier implements it.
type Tokens interface {
	VerifyPreview(token string, now time.Time) (sandboxtoken.PreviewClaims, error)
}

// Nodes leases a node's vmmd client; gateway.NodeClientCache implements it.
type Nodes interface {
	ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool)
}

// Handler serves preview hosts under Domain. Now is time.Now unless set.
type Handler struct {
	Domain string
	Routes Routes
	Tokens Tokens
	Nodes  Nodes
	Log    *slog.Logger
	Now    func() time.Time

	once    sync.Once
	proxy   *httputil.ReverseProxy
	group   singleflight.Group
	mu      sync.Mutex
	routes  map[string]cachedRoute
	session map[string]*sessionUse
}

type cachedRoute struct {
	route   Route
	found   bool
	expires time.Time
}

// sessionUse is one session's request rate and open connections.
type sessionUse struct {
	limiter *rate.Limiter
	conns   int
	used    time.Time
}

// target is where one proxied request goes.
type target struct {
	sandboxID, nodeID, instanceID string
	port                          int
}

type targetKey struct{}

// Match reports whether host is one of this handler's preview hosts.
func (h *Handler) Match(host string) bool {
	_, _, ok := api.ParseSandboxPreviewHost(hostOnly(host), h.Domain)
	return ok
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(h.init)
	port, id, ok := api.ParseSandboxPreviewHost(hostOnly(r.Host), h.Domain)
	if !ok {
		http.NotFound(w, r)
		return
	}
	route, found, err := h.route(r.Context(), id)
	if err != nil {
		h.log().Warn("sandbox preview: read route", "sandbox_id", id, "err", err)
		writeUnavailable(w, "the preview could not be routed; retry")
		return
	}
	visibility := route.Ports[port]
	if !found || !route.Live || visibility == "" {
		http.NotFound(w, r)
		return
	}
	if visibility != api.SandboxPortPublic && !h.authorize(w, r, id, port, route) {
		return
	}
	if route.NodeID == "" || route.InstanceID == "" {
		w.Header().Set("Retry-After", "1")
		writeUnavailable(w, "the sandbox session is still starting")
		return
	}
	idx := route.Plan.PlanIndex()
	if !h.allow(id, api.SandboxPreviewRequestsPerSec[idx]) {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeAgentLimit, "Preview rate limit reached",
			"the session's previews got more requests than SandboxPreviewRequestsPerSec allows").
			WithLimit(int64(api.SandboxPreviewRequestsPerSec[idx]), int64(api.SandboxPreviewRequestsPerSec[idx])+1))
		return
	}
	stripPlatform(r)
	ctx := context.WithValue(r.Context(), targetKey{}, target{sandboxID: id, nodeID: route.NodeID, instanceID: route.InstanceID, port: port})
	h.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// authorize checks a private port's preview token: a bearer token, the
// preview cookie, or the query parameter a browser opens the URL with once,
// which is swapped for the cookie by a redirect. A token for another
// session, port or account answers like an absent route.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, id string, port int, route Route) bool {
	valid := func(token string) (sandboxtoken.PreviewClaims, bool) {
		if h.Tokens == nil || token == "" {
			return sandboxtoken.PreviewClaims{}, false
		}
		c, err := h.Tokens.VerifyPreview(token, h.now())
		return c, err == nil && c.SandboxID == id && c.Port == port && c.AccountID == route.AccountID
	}
	if token := r.URL.Query().Get(api.SandboxPreviewTokenParam); token != "" {
		c, ok := valid(token)
		if !ok {
			http.NotFound(w, r)
			return false
		}
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Expires: c.ExpiresAt, MaxAge: max(1, int(c.ExpiresAt.Sub(h.now()).Seconds()))})
		q := r.URL.Query()
		q.Del(api.SandboxPreviewTokenParam)
		next := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
		if next.Path == "" {
			next.Path = "/"
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, next.RequestURI(), http.StatusSeeOther)
		return false
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if _, ok := valid(bearer); ok {
			r.Header.Del("Authorization")
			return true
		}
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		if _, ok := valid(cookie.Value); ok {
			return true
		}
	}
	http.NotFound(w, r)
	return false
}

// stripPlatform removes what is the platform's from a request before the
// guest sees it: the preview cookie, the token parameter, and the
// platform's own headers.
func stripPlatform(r *http.Request) {
	if cookies := r.Cookies(); len(cookies) > 0 {
		r.Header.Del("Cookie")
		for _, c := range cookies {
			if c.Name != CookieName {
				r.AddCookie(c)
			}
		}
	}
	for name := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-faas-") || strings.HasPrefix(lower, "x-gregale-") || strings.HasPrefix(lower, "faas-") {
			r.Header.Del(name)
		}
	}
}

func (h *Handler) init() {
	transport := &http.Transport{
		DialContext: h.dial,
		// Every request is its own guest connection: an idle pooled
		// connection would hold the session busy and keep it from freezing.
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     false,
	}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "sandbox-preview"
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler:  h.proxyError,
	}
	h.routes = map[string]cachedRoute{}
	h.session = map[string]*sessionUse{}
}

// dial opens a port_connect call to the request's guest port, within the
// session's connection cap.
func (h *Handler) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	t, ok := ctx.Value(targetKey{}).(target)
	if !ok {
		return nil, errors.New("sandboxpreview: no target")
	}
	if !h.acquireConn(t.sandboxID) {
		return nil, errConnCap
	}
	release := func() { h.releaseConn(t.sandboxID) }
	client, closer, ok := h.Nodes.ClientFor(ctx, t.nodeID)
	if !ok {
		release()
		return nil, errNodeUnreachable
	}
	// The call outlives the dial: it ends when the connection closes.
	callCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	fail := func(err error) (net.Conn, error) {
		cancel()
		_ = closer.Close()
		release()
		return nil, err
	}
	stream, err := client.SandboxCall(callCtx)
	if err != nil {
		return fail(err)
	}
	req := sandboxproto.Request{Op: sandboxproto.OpPortConnect, Port: t.port}
	if err := req.Validate(); err != nil {
		return fail(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fail(err)
	}
	start := &vmmdpb.SandboxCallStart{Instance: t.instanceID, SandboxId: t.sandboxID, Request: body}
	if err := stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Start{Start: start}}); err != nil {
		return fail(err)
	}
	return &streamConn{stream: stream, cancel: cancel, onClose: func() { _ = closer.Close(); release() }}, nil
}

var (
	errConnCap         = errors.New("sandboxpreview: the session's preview connection cap is reached")
	errNodeUnreachable = errors.New("sandboxpreview: the session's node is unreachable")
)

func (h *Handler) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errPortClosed):
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeSandboxPortClosed, "Nothing listens on the port",
			"the sandbox session exposes this port, but nothing in it listens there; start the server on 0.0.0.0 or 127.0.0.1"))
	case errors.Is(err, errConnCap):
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeAgentLimit, "Preview connection cap reached",
			"the session's previews hold SandboxPreviewConnsPerSession connections").
			WithLimit(int64(api.SandboxPreviewConnsPerSession), int64(api.SandboxPreviewConnsPerSession)))
	case errors.Is(err, context.Canceled):
		// The client went away.
	default:
		h.log().Warn("sandbox preview: proxy", "host", r.Host, "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Preview unavailable",
			"the sandbox session did not answer the preview request"))
	}
}

// route returns a session's cached route, reading it once per miss.
func (h *Handler) route(ctx context.Context, id string) (Route, bool, error) {
	now := h.now()
	h.mu.Lock()
	c, ok := h.routes[id]
	h.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.route, c.found, nil
	}
	v, err, _ := h.group.Do(id, func() (any, error) {
		route, err := h.Routes.PreviewRoute(context.WithoutCancel(ctx), id)
		found := err == nil
		if err != nil && !errors.Is(err, ErrNoRoute) {
			return nil, err
		}
		c := cachedRoute{route: route, found: found, expires: h.now().Add(RouteCacheTTL)}
		h.mu.Lock()
		h.prune(now)
		h.routes[id] = c
		h.mu.Unlock()
		return c, nil
	})
	if err != nil {
		return Route{}, false, err
	}
	c = v.(cachedRoute)
	return c.route, c.found, nil
}

// pruneAt bounds the caches; past it, expired routes and idle sessions are
// dropped. Called with mu held.
const pruneAt = 4096

func (h *Handler) prune(now time.Time) {
	if len(h.routes) >= pruneAt {
		for id, c := range h.routes {
			if !now.Before(c.expires) {
				delete(h.routes, id)
			}
		}
	}
	if len(h.session) >= pruneAt {
		for id, s := range h.session {
			if s.conns == 0 && now.Sub(s.used) > time.Minute {
				delete(h.session, id)
			}
		}
	}
}

func (h *Handler) use(id string, perSec int) *sessionUse {
	s, ok := h.session[id]
	if !ok {
		s = &sessionUse{limiter: rate.NewLimiter(rate.Limit(perSec), max(1, 2*perSec))}
		h.session[id] = s
	}
	if s.limiter.Limit() != rate.Limit(perSec) {
		s.limiter.SetLimit(rate.Limit(perSec))
		s.limiter.SetBurst(max(1, 2*perSec))
	}
	s.used = h.now()
	return s
}

func (h *Handler) allow(id string, perSec int) bool {
	if perSec <= 0 {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.use(id, perSec).limiter.AllowN(h.now(), 1)
}

func (h *Handler) acquireConn(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.session[id]
	if !ok || s.conns >= api.SandboxPreviewConnsPerSession {
		return false
	}
	s.conns++
	return true
}

func (h *Handler) releaseConn(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.session[id]; ok && s.conns > 0 {
		s.conns--
	}
}

func writeUnavailable(w http.ResponseWriter, detail string) {
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeInternal, "Preview unavailable", detail))
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
