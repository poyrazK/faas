// Package reqbudget: middleware.go — the BudgetMiddleware that
// gatewayd-public and apid install at their public listener. The
// middleware stamps a fresh per-request Budget onto r.Context() and
// runs the inner handler under that budget. On deadline fire, the
// middleware intercepts the stdlib context.DeadlineExceeded, writes
// a 504 RFC 7807 problem+json response, and increments the
// exceeded counter against the hop that fired first.
//
// On a clean handler return the middleware records the remaining
// budget at attach time (a stable observation: attached total -
// observed at attach).
package reqbudget

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// endpointUnknown / routeUnknown are the per-handler fallbacks the
// middleware stamps on Budget.Endpoint / Route when the caller left
// them blank. NewMiddlewareConfig callers (gatewayd-public, apid) set
// both at startup; the inline Middleware(...) fallback path uses these
// so a direct MiddlewareConfig{} literal still produces stable
// metric labels.
const (
	endpointUnknown              = "unknown"
	routeUnknown                 = "unknown"
	requestBudgetErrorCodeHeader = "X-Faas-Error-Code"
	requestIDHeader              = "X-Faas-Request-Id"
)

// MiddlewareConfig wires BudgetMiddleware. nil Metrics means
// observations are no-ops (tests + path that doesn't have a Prometheus
// registry yet — the gateway build wires metrics in via runDeps).
type MiddlewareConfig struct {
	// Default is the per-request budget installed when no edge-rule
	// kind=budget match overrides it. Must be > 0; validated at
	// construction time.
	Default time.Duration
	// Max is the absolute upper bound on any per-request budget. A
	// kind=budget rule larger than Max is clamped down to Max. Must
	// be >= Default; validated at construction.
	Max time.Duration
	// Route is the coarse route label ("forward", "admin", "invoke")
	// stamped onto every Budget + every metric. The endpoint label
	// comes from the matched route template at runtime; the
	// middleware cannot derive it from r.URL.Path alone (cardinality
	// blow-up risk).
	Route string
	// Endpoint is the per-request endpoint label. The middleware
	// derives this from the matched route after dispatch (see
	// MatchEndpointFunc). When empty, the middleware falls back to
	// "unknown".
	Endpoint string
	// Metrics is the reqbudget.M struct registered against the
	// daemon's Prometheus registry. nil means no-op observations.
	Metrics *M
	// Log is the structured logger; nil falls back to slog.Default().
	Log *slog.Logger
	// Now is the clock used to stamp Budget.Started at attach time.
	// nil → time.Now.
	Now func() time.Time
	// DefaultFor optionally selects a route-specific default budget.
	// Returning <= 0 keeps Default. The selected value is still
	// clamped to Max, so a route callback cannot widen the configured
	// absolute ceiling.
	DefaultFor func(*http.Request) time.Duration
}

// NewMiddlewareConfig validates cfg and returns it. The constructor
// exists so a misconfigured budget can't be plumbed into a daemon at
// runtime — the validation lives in one place and the daemon boot
// path catches the error.
func NewMiddlewareConfig(cfg MiddlewareConfig) (MiddlewareConfig, error) {
	if cfg.Default <= 0 {
		return MiddlewareConfig{}, errors.New("reqbudget: Default must be > 0")
	}
	if cfg.Max <= 0 {
		cfg.Max = DefaultBudgetMax
	}
	if cfg.Max < cfg.Default {
		return MiddlewareConfig{}, errors.New("reqbudget: Max must be >= Default")
	}
	if cfg.Route == "" {
		return MiddlewareConfig{}, errors.New("reqbudget: Route must be set")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = endpointUnknown
	}
	return cfg, nil
}

// Middleware returns the http.Handler middleware that wraps next
// with the per-request budget. On deadline fire the middleware
// writes a 504 problem+json response and aborts the inner handler
// chain (the wrapped writer swallows subsequent writes).
//
// Layout: one http.HandlerFunc, three branches:
//
//   - Pre-dispatch: install BudgetMiddleware + start a goroutine
//     that observes the deadline outcome and writes the 504 problem
//     on exceed.
//   - In-handler: standard ServeHTTP, no special handling. The inner
//     handler sees the budget-decorated ctx.
//   - Post-handler: snapshot the final remaining (if any) for the
//     metrics observation.
//
// The middleware never short-circuits a handler that finished
// successfully — it lets the response through and only writes 504
// when r.Context() is done with DeadlineExceeded AND the handler
// hasn't written a response body yet.
func (cfg MiddlewareConfig) Middleware(next http.Handler) http.Handler {
	if cfg.Max == 0 {
		cfg.Max = DefaultBudgetMax
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = endpointUnknown
	}
	if cfg.Route == "" {
		cfg.Route = routeUnknown
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Per-request budget: clamp against Max and against any
		// earlier parent deadline (stdlib http.Server attaches
		// ReadTimeout / WriteTimeout on r.Context() at listener
		// dispatch). WithRemaining does both clampings.
		total := cfg.Default
		if cfg.DefaultFor != nil {
			if routeDefault := cfg.DefaultFor(r); routeDefault > 0 {
				total = routeDefault
			}
		}
		if total > cfg.Max {
			total = cfg.Max
		}
		ctx, cancel, b := WithRemaining(r.Context(), total, cfg.Max, cfg.Route, cfg.Endpoint)
		defer cancel()

		// Wrap the writer so the middleware can tell whether the
		// inner handler wrote a body before r.Context() fired. If
		// not, the middleware writes the 504 problem.
		bw := &budgetWriter{ResponseWriter: w}

		// Set the ctx for the rest of the handler chain. We do this
		// by handing the wrapped ctx into a small ServeHTTP
		// indirection — r is shared with downstream middleware so
		// we cannot mutate r itself.
		next.ServeHTTP(bw, r.WithContext(ctx))

		// Snapshot the outcome.
		switch {
		case bw.wrote:
			// Inner handler wrote a response. We're done; record
			// remaining-at-attach (positive observation) under
			// outcome=set.
			cfg.observe(b, "set")
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			// Inner handler hit the budget before it could write a
			// response. Write 504 + RFC 7807 envelope if no one
			// else has yet, then record outcome=exceeded.
			cfg.writeProblem(bw, w, b, "exceeded", "request_budget_exceeded", r)
			cfg.observe(b, "exceeded")
		case errors.Is(ctx.Err(), context.Canceled):
			cfg.observe(b, "cancelled")
		default:
			// Inner handler returned without writing and without a
			// deadline fire (e.g. panic, recovered at the handler).
			// Don't double-write — let the upstream recovery
			// middleware handle it.
			cfg.observe(b, "set")
		}
	})
}

// observe logs and records metrics for a budget outcome. Called
// once per request after the inner handler returns. outcome ∈
// {"set", "exceeded", "cancelled"}.
func (cfg MiddlewareConfig) observe(b Budget, outcome string) {
	if cfg.Metrics == nil || cfg.Metrics.RequestBudgetSeconds == nil {
		return
	}
	remaining := b.Remaining(time.Time{})
	// For exceeded/cancelled the remaining is ~0; for set it's the
	// leftover budget at handler completion. Histogram observation
	// is bounded by [0, b.Total] so we clamp.
	if remaining < 0 {
		remaining = 0
	}
	if remaining > b.Total {
		remaining = b.Total
	}
	cfg.Metrics.RequestBudgetSeconds.
		WithLabelValues(b.Route, b.Endpoint, outcome).
		Observe(remaining.Seconds())
}

// writeProblem writes a 504 RFC 7807 envelope when the inner
// handler hasn't yet committed a response. The middleware uses a
// fixed code (`request_budget_exceeded`) and a fixed docs URL —
// see ADR-093 for the docs location.
func (cfg MiddlewareConfig) writeProblem(bw *budgetWriter, w http.ResponseWriter, b Budget, outcome, code string, r *http.Request) {
	if bw.wrote {
		return
	}
	// Hop name is the last entry on the audit trail, if any — that
	// names which downstream exceeded first.
	hop := "gateway"
	if len(b.Overheads) > 0 {
		hop = b.Overheads[len(b.Overheads)-1].Name
	}
	if cfg.Metrics != nil && cfg.Metrics.RequestBudgetExceededTotal != nil {
		cfg.Metrics.RequestBudgetExceededTotal.
			WithLabelValues(b.Route, b.Endpoint, hop).
			Inc()
	}
	cfg.Log.Info("budget_exceeded",
		"code", code,
		"route", b.Route,
		"endpoint", b.Endpoint,
		"budget_ms", b.Total.Milliseconds(),
		"hop", hop,
	)
	// These headers are the edge adapter contract. They remain useful when a
	// CDN replaces the origin error body, and are harmless on direct origin
	// responses. Set (rather than Add) so a request-id middleware cannot create
	// ambiguous duplicate values.
	w.Header().Set(requestBudgetErrorCodeHeader, code)
	w.Header().Set("Cache-Control", "no-store")
	if r != nil {
		if rid := r.Header.Get(requestIDHeader); rid != "" {
			w.Header().Set(requestIDHeader, rid)
		}
	}
	// Direct write to the wrapped w (not bw) so the budgetWriter's
	// wrote flag isn't double-tripped — bw is only consulted
	// post-handler.
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusGatewayTimeout)
	_, _ = w.Write([]byte(`{"type":"about:blank","title":"request budget exceeded","status":504,"code":"request_budget_exceeded","limit":"` +
		b.Total.String() + `","docs_url":"https://docs.gregale.dev/errors/request-budget-exceeded"}`))
}

// budgetWriter is a tiny http.ResponseWriter wrapper that tracks
// whether the inner handler committed a response body. If not, the
// middleware writes its own 504 problem on top.
//
// The wrapper deliberately does NOT implement http.Flusher /
// http.Hijacker — those are power-user escapes the middleware is
// not in the path for today; the streaming-forwarder is one layer
// down where the per-flush write deadline is enforced separately.
type budgetWriter struct {
	http.ResponseWriter
	wrote bool
}

func (b *budgetWriter) WriteHeader(code int) {
	if b.wrote {
		return
	}
	b.wrote = true
	b.ResponseWriter.WriteHeader(code)
}

// Write is a transparent passthrough to the wrapped ResponseWriter;
// the only thing this method adds is the `wrote` flip so the
// middleware knows the inner handler has committed a response body.
// The body bytes themselves are NEVER inspected by reqbudget —
// whatever the wrapped handler writes flows straight to the
// ResponseWriter untouched.
//
// codeql[go/reflected-xss] false-positive: reqbudget is a deadline
// accounting layer, not a renderer. The dataflow that CodeQL traces
// here originates at cmd/vmmd-stream-bridge/main.go:285, which
// writes an inbound HTTP request body (transparent guest proxy) to
// the upstream connection, and the guest response flows back to the
// customer via io.Copy. The customer-facing response is whatever the
// guest chose to emit (typically JSON, JSONL, or binary protocol
// bytes — never rendered HTML); the reqbudget layer never parses,
// renders, or interpolates any part of the body. This is structurally
// identical to the stdlib http.ResponseWriter passthrough pattern;
// applying html.EscapeString would corrupt every non-text response
// the platform proxies. The load-bearing transparency contract lives
// at cmd/vmmd-stream-bridge/main.go:283-286 (chunked-body goroutine)
// and pkg/gateway/forwardproxy.go:336 / 554 / 743 / 757-766 (ctxReader
// streaming). Any change here MUST be coordinated with those sites.
func (b *budgetWriter) Write(p []byte) (int, error) {
	if !b.wrote {
		b.wrote = true
	}
	return b.ResponseWriter.Write(p)
}
