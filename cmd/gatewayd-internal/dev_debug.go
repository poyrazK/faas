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

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

type devDebugTargets interface {
	WakeTarget(ctx context.Context, appID string, port int) (gateway.Target, error)
}

type devDebugTunnel struct {
	auth     *mwauth.Middleware
	targets  atomic.Pointer[devDebugTargets]
	forward  func(context.Context, net.Conn, gateway.Target) error
	sessions devDebugSessions
	log      *slog.Logger
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
	inner := mwauth.AccountHandler(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h.attach(w, r, acct, slug)
	})
	chain := h.auth.RequireScope(api.ScopesDeployWriteSurface...)(h.auth.RequireMFA(inner))
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
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
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
	upgrader := websocket.Upgrader{
		// The CLI sends no Origin. Refusing browser origins stops a web page
		// from opening the tunnel with an ambient credential.
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
	}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := devbridge.NewWebSocketConn(socket)
	defer func() { _ = conn.Close() }()
	h.log.Info("developer debugger attached", "account_id", acct.ID, "app_id", app.ID, "instance_id", target.InstanceID)
	if err := h.forward(r.Context(), conn, target); err != nil && r.Context().Err() == nil {
		h.log.Info("developer debugger session ended", "app_id", app.ID, "err", err)
	}
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
	return &devDebugTunnel{auth: deps.authMw, forward: forwarder.ServeConn, log: log}
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
