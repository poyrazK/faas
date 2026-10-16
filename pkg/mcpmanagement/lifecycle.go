package mcpmanagement

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const drainCleanupTimeout = 5 * time.Second

type lifecycleWork struct {
	cancel      context.CancelFunc
	observation bool
}

// Lifecycle coordinates draining across all servers and HTTP handlers sharing it.
// Observations stop immediately; active mutations and HTTP journal commits get grace.
type Lifecycle struct {
	mu       sync.Mutex
	draining bool
	work     map[*lifecycleWork]struct{}
	drained  chan struct{}
	closed   bool
}

// NewLifecycle creates a coordinator with open admission and no active work.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{work: map[*lifecycleWork]struct{}{}, drained: make(chan struct{})}
}

func (l *Lifecycle) enter(ctx context.Context, observation bool) (context.Context, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if l.draining {
		retry := int64(1)
		return nil, nil, &api.APIError{Problem: api.Problem{Status: 503, Code: "mcp_draining", Title: "MCP bridge draining", Detail: "This bridge is shutting down; reconnect after restart", Hint: "Retry with the same plan ID when durable state is retained", RetryAfterSeconds: &retry}}
	}
	ctx, cancel := context.WithCancel(ctx)
	work := &lifecycleWork{cancel: cancel, observation: observation}
	l.work[work] = struct{}{}
	return ctx, func() { cancel(); l.mu.Lock(); delete(l.work, work); l.closeIfDrained(); l.mu.Unlock() }, nil
}
func (l *Lifecycle) cancelAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.draining = true
	for work := range l.work {
		work.cancel()
	}
	l.closeIfDrained()
}

func (l *Lifecycle) closeIfDrained() {
	if l.draining && len(l.work) == 0 && !l.closed {
		close(l.drained)
		l.closed = true
	}
}

// BeginDrain atomically stops admission and cancels observations; it is idempotent.
func (l *Lifecycle) BeginDrain() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.draining = true
	for work := range l.work {
		if work.observation {
			work.cancel()
		}
	}
	l.closeIfDrained()
}

// Drained reports whether no tracked handler or subscription worker remains.
func (l *Lifecycle) Drained() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.draining && len(l.work) == 0
}

// Shutdown waits for grace, then cancels remaining work and waits up to five
// seconds for cleanup. A non-nil result means state must remain open until cleanup.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	l.BeginDrain()
	select {
	case <-l.drained:
		return nil
	case <-ctx.Done():
	}
	l.mu.Lock()
	for work := range l.work {
		work.cancel()
	}
	l.mu.Unlock()
	timer := time.NewTimer(drainCleanupTimeout)
	defer timer.Stop()
	select {
	case <-l.drained:
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func registerLifecycle(s *mcp.Server, m *management) {
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			observation := true
			switch method {
			case "tools/call":
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
					observation = !m.toolWrites[p.Name]
				}
			case "resources/read", "prompts/get", "completion/complete", "subscriptions/listen":
			default:
				return next(ctx, method, req)
			}
			ctx, finish, err := m.lifecycle.enter(ctx, observation)
			if err != nil {
				if method == "tools/call" {
					return m.toolError(err), nil
				}
				return nil, m.contextError(err)
			}
			defer finish()
			grantCtx, finishGrant, err := m.grantLifecycle.enter(ctx, observation)
			if err != nil {
				if ctx.Err() == nil {
					err = &api.APIError{Problem: api.Problem{Status: 403, Code: "mcp_connection_revoked", Title: "Agent connection revoked", Detail: "Reconnect and review consent before using this connection again"}}
				}
				if method == "tools/call" {
					return m.toolError(err), nil
				}
				return nil, m.contextError(err)
			}
			defer finishGrant()
			return next(grantCtx, method, req)
		}
	})
}

func (l *Lifecycle) wrapHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, finish, err := l.enter(r.Context(), false)
		if err != nil {
			setPrivateHeaders(w)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "MCP bridge draining; reconnect after restart", http.StatusServiceUnavailable)
			return
		}
		defer finish()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
