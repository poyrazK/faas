package main

// ADR-741: `gregale dev --debug` reaches the Node inspector of a developer
// environment through this authenticated WebSocket tunnel. The inspector
// port is never published at the edge; the only path to it is here, where
// the caller must hold a deploy-scoped key for an account that owns the app,
// and the app must be a `gregale dev` environment.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type devDebugTargets interface {
	WakeTarget(ctx context.Context, appID string, port int) (gateway.Target, error)
}

type devDebugTunnel struct {
	auth     *mwauth.Middleware
	targets  atomic.Pointer[devDebugTargets]
	forward  func(context.Context, net.Conn, gateway.Target) error
	sessions devDebugSessions
	audits   devDebugAudits
	log      *slog.Logger
	// traffic enrolls the tunnel under the app's security scopes
	// (admitPlatformTraffic); nil only in legacy fixtures.
	traffic *trafficrevocation.Registry
}

// setTargets wires the service proxy once gatewayd-internal has built it;
// the tunnel answers 503 until then.
func (h *devDebugTunnel) setTargets(targets devDebugTargets) {
	h.targets.Store(&targets)
}

// isDevDebugPath matches exactly /v1/apps/{slug}/debug.
func isDevDebugPath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/v1/apps/")
	if !ok {
		return "", false
	}
	slug, tail, ok := strings.Cut(rest, "/")
	return slug, ok && slug != "" && tail == "debug"
}

// devDebugRoute sends the debugger tunnel path to h and everything else to
// next, ahead of the apid proxy.
func devDebugRoute(h *devDebugTunnel, next http.Handler) http.Handler {
	if h == nil || h.auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slug, ok := isDevDebugPath(r.URL.Path); ok {
			h.serve(w, r, slug)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *devDebugTunnel) serve(w http.ResponseWriter, r *http.Request, slug string) {
	r.SetPathValue("slug", slug)
	r.Pattern = r.Method + " /v1/apps/{slug}/debug"
	inner := mwauth.AccountHandler(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h.attach(w, r, acct, slug)
	})
	chain := h.auth.RequireScope(api.ScopesDeployWriteSurface...)(h.auth.RequireMFA(wrapControlPlaneAppAccess(h.auth, inner)))
	h.auth.RequireLimited(chain)(w, r)
}

func (h *devDebugTunnel) attach(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) {
	if r.Method != http.MethodGet || !websocket.IsWebSocketUpgrade(r) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"WebSocket required", "the debugger tunnel accepts only a WebSocket upgrade; use `gregale dev --debug`"))
		return
	}
	app, ok := h.auth.LoadApp(w, r, acct, slug)
	if !ok {
		return
	}
	if !state.IsDeveloperApp(app) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDevDebugNotDeveloperEnvironment,
			"Debugging unavailable", "a debugger can attach only to a `gregale dev` environment"))
		return
	}
	targetsPtr := h.targets.Load()
	if targetsPtr == nil {
		api.WriteProblem(w, api.ErrCapacity("developer debugger"))
		return
	}
	release, admitted := h.sessions.acquire(app.ID, api.DevDebugSessionsPerApp)
	if !admitted {
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeDevDebugSessionLimit,
			"Too many debugger sessions", "this developer environment already has the maximum number of debugger connections"))
		return
	}
	defer release()
	wakeCtx, cancel := context.WithTimeout(r.Context(), api.DevDebugWakeTimeout)
	target, err := (*targetsPtr).WakeTarget(wakeCtx, app.ID, api.DevDebugNodePort)
	cancel()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("wake developer environment for debugging"))
		return
	}
	upgradeHeader := http.Header{}
	ctx, releaseTraffic, ok := admitPlatformTraffic(r.Context(), w, r, h.traffic, upgradeHeader, acct.ID, app.ID)
	if !ok {
		return
	}
	defer releaseTraffic()
	upgrader := websocket.Upgrader{
		// The CLI sends no Origin. Refusing browser origins stops a web page
		// from opening the tunnel with an ambient credential.
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
	}
	socket, err := upgrader.Upgrade(w, r, upgradeHeader)
	if err != nil {
		return
	}
	conn := devbridge.NewWebSocketConn(socket)
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	// A debugger session can evaluate code and read the process environment,
	// so it leaves an audit trail like any other privileged access: who
	// attached (account and key, including agent keys), to which environment
	// and instance, and for how long.
	auditData := map[string]any{"app_id": app.ID, "app_slug": app.Slug, "instance_id": target.InstanceID}
	keyID := ""
	if _, key, ok := mwauth.AccountFromCtx(r.Context()); ok && key != nil {
		keyID = key.ID
		auditData["key_id"] = key.ID
	}
	// The session's events outlive this request (detached fires after the
	// grace), so they carry a context that is not cancelled with it.
	endAudit := h.audits.open(context.WithoutCancel(r.Context()), devDebugAuditKey{accountID: acct.ID, appID: app.ID, keyID: keyID}, auditData,
		func(ctx context.Context, kind string, data map[string]any) {
			h.emitAudit(ctx, kind, acct.ID, data)
		})
	defer endAudit()
	h.log.Info("developer debugger attached", "account_id", acct.ID, "app_id", app.ID, "instance_id", target.InstanceID)
	if err := h.forward(ctx, conn, target); err != nil && ctx.Err() == nil {
		h.log.Info("developer debugger session ended", "app_id", app.ID, "err", err)
	}
}

func (h *devDebugTunnel) emitAudit(ctx context.Context, kind, accountID string, data map[string]any) {
	if h.auth == nil || h.auth.Audit == nil {
		return
	}
	h.auth.Audit.Emit(ctx, kind, &accountID, data)
}

// devDebugAuditGrace is how long the last tunnel of a debugging session may
// be closed before the session counts as detached. Debuggers poll target
// discovery about every second, and each poll the CLI's two-second cache
// misses opens a short tunnel: auditing each one wrote an attached/detached
// pair per poll, about two rows a second for as long as chrome://inspect was
// open (found on prod 2026-11-11).
const devDebugAuditGrace = 60 * time.Second

type devDebugAuditKey struct{ accountID, appID, keyID string }

type devDebugAuditSession struct {
	open, tunnels int
	started       time.Time
	attached      map[string]any
	ctx           context.Context
	emit          func(ctx context.Context, kind string, data map[string]any)
	detach        func() bool // stops the pending detached event
}

// devDebugAudits coalesces tunnels into debugging sessions per account, app
// and key: dev.debugger.attached when the first tunnel opens, and
// dev.debugger.detached once none has been open for devDebugAuditGrace, with
// the session's duration and tunnel count.
type devDebugAudits struct {
	mu       sync.Mutex
	sessions map[devDebugAuditKey]*devDebugAuditSession
	now      func() time.Time
	after    func(time.Duration, func()) func() bool
}

func (a *devDebugAudits) clock() (func() time.Time, func(time.Duration, func()) func() bool) {
	now, after := a.now, a.after
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	return now, after
}

// open records one tunnel and returns the function its close calls.
func (a *devDebugAudits) open(ctx context.Context, key devDebugAuditKey, data map[string]any, emit func(ctx context.Context, kind string, data map[string]any)) func() {
	now, after := a.clock()
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = map[devDebugAuditKey]*devDebugAuditSession{}
	}
	s := a.sessions[key]
	fresh := s == nil
	if fresh {
		s = &devDebugAuditSession{started: now(), attached: data, ctx: ctx, emit: emit}
		a.sessions[key] = s
	} else if s.detach != nil {
		s.detach()
		s.detach = nil
	}
	s.open++
	s.tunnels++
	a.mu.Unlock()
	if fresh {
		emit(ctx, "dev.debugger.attached", data)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if s.open--; s.open > 0 {
				return
			}
			s.detach = after(devDebugAuditGrace, func() { a.finish(key, s) })
		})
	}
}

func (a *devDebugAudits) finish(key devDebugAuditKey, s *devDebugAuditSession) {
	now, _ := a.clock()
	a.mu.Lock()
	if a.sessions[key] != s || s.open > 0 {
		a.mu.Unlock()
		return
	}
	delete(a.sessions, key)
	detached := make(map[string]any, len(s.attached)+2)
	for k, v := range s.attached {
		detached[k] = v
	}
	// The session ended when its last tunnel closed, a grace period ago.
	detached["duration_ms"] = now().Add(-devDebugAuditGrace).Sub(s.started).Milliseconds()
	detached["tunnels"] = s.tunnels
	a.mu.Unlock()
	s.emit(s.ctx, "dev.debugger.detached", detached)
}

// newDevDebugTunnel returns nil when the gateway lacks the API-key
// middleware or the vmmd node cache; the path then falls through to the
// apid proxy and is not served.
func newDevDebugTunnel(deps runDeps, log *slog.Logger) *devDebugTunnel {
	if deps.authMw == nil || deps.nodeCache == nil {
		return nil
	}
	forwarder := gateway.TCPForwarder{Nodes: deps.nodeCache.cache, MaxBytes: api.DevDebugMaxBytes, IdleTimeout: api.DevDebugIdleTimeout}
	if deps.metrics != nil {
		forwarder.Metrics = tcpmetrics.New(deps.metrics.Registry(), "gatewayd_internal_dev_debug")
	}
	return &devDebugTunnel{auth: deps.authMw, forward: forwarder.ServeConn, log: log, traffic: deps.trafficRevocations}
}

// devDebugSessions bounds concurrent debugger connections per app.
type devDebugSessions struct {
	mu      sync.Mutex
	current map[string]int
}

func (s *devDebugSessions) acquire(appID string, limit int) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		s.current = map[string]int{}
	}
	if s.current[appID] >= limit {
		return func() {}, false
	}
	s.current[appID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.current[appID]--; s.current[appID] <= 0 {
				delete(s.current, appID)
			}
		})
	}, true
}
