package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// App is the routing target for a hostname.
type App struct {
	ID   string
	Plan api.Plan
}

// Backend is the seam between the edge and the rest of the platform (in
// production: the routing cache over Postgres, and schedd over gRPC). Splitting
// it out keeps the hot request path testable end-to-end without a real cluster.
type Backend interface {
	// Lookup resolves a hostname to its app (cache-first, spec §4.1).
	Lookup(ctx context.Context, host string) (App, bool)
	// Target returns a ready instance address (host:port) for the app, or false
	// when none is running and a wake is needed (the hot path returns true here).
	Target(appID string) (string, bool)
	// Wake ensures an instance is running via schedd admission + vmmd restore.
	Wake(ctx context.Context, appID string) error
}

// Handler is gatewayd's HTTP entrypoint: route → rate-limit → (wake-block if
// parked) → proxy (spec §4.1, §2). It is the only public listener on the box.
type Handler struct {
	backend Backend
	limiter *Limiter
	gate    *WakeGate
	// metrics may be nil; nil-guarded everywhere it is read.
	metrics *Metrics
	// log may be nil (defaults to slog.Default()).
	log *slog.Logger
	// appsSuffix is the configured apps.DOMAIN suffix (e.g. ".apps.example.com").
	// Non-empty enables a pre-Lookup host suffix check that 404s anything
	// outside it (spec §4.1 noise filter). Custom domains (Pro+) bypass this
	// constraint implicitly by being keys in the routing cache — see
	// WithAppsSuffix docs.
	appsSuffix string
	// proxyFor builds the reverse proxy for an upstream address; overridable in
	// tests.
	proxyFor func(addr string) http.Handler
	// lastSeen records per-instance last_request_at (spec §4.1). nil-safe.
	lastSeen LastSeenSink
}

// NewHandler wires the edge with the spec's defaults (wake queue 512/30 s, spec
// §4.1) and the new Metrics + slog bundle. The host→app routing cache lives
// inside the Backend (it fronts Postgres).
func NewHandler(backend Backend) *Handler {
	return NewHandlerWith(backend, NewMetrics(), slog.Default())
}

// NewHandlerWith lets tests inject a custom Metrics bundle (to assert on the
// registry) and a custom slog logger.
func NewHandlerWith(backend Backend, m *Metrics, log *slog.Logger) *Handler {
	h := &Handler{
		backend: backend,
		limiter: NewLimiter(),
		gate:    NewWakeGate(api.WakeQueueCap, time.Duration(api.WakeQueueTTLSeconds)*time.Second),
		metrics: m,
		log:     log,
	}
	h.proxyFor = defaultProxy
	return h
}

// WithAppsSuffix sets the *.apps.DOMAIN suffix filter (call before serving).
// When set, every request whose Host doesn't end in this suffix is rejected
// with 404 BEFORE consulting the cache. Custom domains on a different suffix
// are intended to be reached via the Lookup table directly (M5); this PR only
// adds the wildcard-apps-domain guard.
func (h *Handler) WithAppsSuffix(suffix string) *Handler {
	// Leading dot normalization so callers can pass either form.
	if suffix != "" && suffix[0] != '.' {
		suffix = "." + suffix
	}
	h.appsSuffix = strings.ToLower(suffix)
	return h
}

// WithLastSeenSink installs the LastSeenSink that records per-instance
// last_request_at (spec §4.1). Production wires a PG-flushing impl from
// schedd; tests use the in-memory implementation (idle.go).
func (h *Handler) WithLastSeenSink(sink LastSeenSink) *Handler {
	h.lastSeen = sink
	return h
}

// SetWakeGateHook installs a callback that wakes the queue-depth gauge each
// time WakeGate mutates an entry. Called by main once the gauge exists.
func (h *Handler) SetWakeGateHook() {
	h.gate.onChange = func(appID string, depth int) {
		if h.metrics != nil {
			h.metrics.SetQueueDepth(appID, depth)
		}
	}
}

// Metrics exposes the Prometheus bundle (used by the control listener to mount
// /metrics). May be nil if NewHandler was used and nothing initialized one.
func (h *Handler) Metrics() *Metrics { return h.metrics }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Status-class capture (used for metrics + slog). Doesn't buffer the body
	// or alter the headers — strictly observability.
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = rec

	// Request ID is generated once per request and set on the response BEFORE
	// any error path so even 4xx responses are correlatable. Inbound
	// x-faas-request-id overrides (lets curl/clients supply their own trace).
	rid := r.Header.Get("x-faas-request-id")
	if rid == "" {
		rid = newRequestID()
	}
	w.Header().Set("x-faas-request-id", rid)
	r = r.WithContext(WithRequestID(r.Context(), rid))

	host := hostname(r.Host)

	// Host allowlist suffix check (spec §4.1: *.apps.DOMAIN). Closes the
	// door on stale DNS records that land on the edge post-TLS by rejecting
	// anything not matching the configured suffix before the cache is touched.
	// Set via NewHandlerWithSuffix or WithAppsSuffix; empty suffix disables
	// the check (the Backend.Lookup table is still authoritative).
	if h.appsSuffix != "" && !strings.HasSuffix(host, h.appsSuffix) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
			api.CodeNotFound, "No such app",
			fmt.Sprintf("host %q does not match the configured apps suffix", host)))
		h.observe(r, rec.status, "", "", false, "")
		return
	}

	app, ok := h.backend.Lookup(r.Context(), host)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No such app", fmt.Sprintf("no app is routed to %q", host)))
		h.observe(r, rec.status, "", "", false, "")
		return
	}

	// Per-app rate limit (spec §4.1). Over-limit → 429.
	if !h.limiter.Allow(app.ID, app.Plan) {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
			"Rate limit exceeded", "slow down and retry"))
		if h.metrics != nil {
			h.metrics.ObserveRateLimit(app.ID, string(app.Plan))
		}
		h.observe(r, rec.status, app.ID, string(app.Plan), false, "")
		return
	}

	// Cap request body either direction (spec §4.1).
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxRequestBodyBytes)

	// Hot path: a ready instance already exists.
	addr, ready := h.backend.Target(app.ID)
	cold := false
	wakeStart := time.Now()
	if !ready {
		if err := h.wake(r.Context(), app.ID); err != nil {
			writeWakeError(w, err)
			h.observe(r, rec.status, app.ID, string(app.Plan), false, "")
			return
		}
		if addr, ready = h.backend.Target(app.ID); !ready {
			api.WriteProblem(w, api.ErrCapacity("woke but no instance became ready"))
			h.observe(r, rec.status, app.ID, string(app.Plan), false, "")
			return
		}
		cold = true
	}

	if cold {
		// Cold-wake transparency (UX spec §6): let developers see the penalty.
		w.Header().Set("x-faas-wake", "cold")
	}
	h.proxyFor(addr).ServeHTTP(w, r)
	h.observe(r, rec.status, app.ID, string(app.Plan), cold, addr)
	if cold && h.metrics != nil {
		// Wake latency is "request-received to first upstream byte". For the
		// reverse proxy we approximate that as request-received → handler
		// return; a precise measurement would require observing the proxy's
		// first byte. This approximation is suitable for SLO dashboards.
		h.metrics.ObserveColdWake(app.ID, time.Since(wakeStart))
	}
}

// observe emits one metric increment + one structured log line per request.
// Always called exactly once on the ServeHTTP exit path; missing it would
// skew the §12 dashboard. On a 2xx response it also Touches the LastSeenSink
// so the idle reaper (schedd) knows the instance was active (spec §4.1).
func (h *Handler) observe(r *http.Request, status int, appID, plan string, cold bool, addr string) {
	code := statusClass(status)
	requestID := requestIDFrom(r)
	if h.metrics != nil {
		// Use placeholder labels for the unknown-host path so 404s show up on
		// the dashboard under a sentinel app_id (e.g. "-" or "").
		if appID == "" {
			appID = "-"
			plan = "-"
		}
		h.metrics.ObserveRequest(appID, plan, code)
	}
	(&requestLogger{log: h.log}).Log(appID, code, time.Since(startTime(r)), cold, requestID)

	// Idle reaper hook (spec §4.1): 2xx → the instance is alive. 4xx/5xx are
	// not evidence of activity (a misconfigured client can hammer a dead
	// instance with 401s forever and we'd never park it).
	if h.lastSeen != nil && status >= 200 && status < 300 && addr != "" {
		h.lastSeen.Touch(addr, time.Now())
	}
}

// statusClass turns an HTTP status into a 3-digit label ("200", "404", "503").
func statusClass(status int) string {
	if status < 100 || status > 999 {
		status = http.StatusInternalServerError
	}
	const digits = "0123456789"
	return string([]byte{digits[(status/100)%10], digits[(status/10)%10], digits[status%10]})
}

// statusRecorder is a thin ResponseWriter wrapper that records the HTTP status
// that was written so metrics can label without buffering headers/body.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		// First Write with no explicit WriteHeader → 200.
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// wake holds the request while schedd/vmmd bring an instance up, coalescing
// concurrent requests for the same app into one wake (spec §4.1). The
// shouldWake predicate runs under the gate lock the moment a caller wins the
// leader election, so if a peer's wake has just observed a ready instance we
// don't fire a redundant restore.
func (h *Handler) wake(ctx context.Context, appID string) error {
	return h.gate.Wait(ctx, appID,
		func() bool {
			_, ready := h.backend.Target(appID)
			return !ready
		},
		func(ctx context.Context) error {
			return h.backend.Wake(ctx, appID)
		},
	)
}

func writeWakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrQueueFull):
		w.Header().Set("Retry-After", "5")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Briefly at capacity", "the wake queue is full; retry shortly"))
	default:
		var prob *api.Problem
		if errors.As(err, &prob) {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("wake failed"))
	}
}

// defaultProxy returns a reverse proxy to addr (spec §4.1: 60 s to first
// response byte). The spec's "25 MB either direction" outbound cap is enforced
// by Server.MaxResponseBodyBytes on the http.Server wrapping this handler, so
// it doesn't need to live inside the proxy itself.
func defaultProxy(addr string) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = &http.Transport{
		ResponseHeaderTimeout: 60 * time.Second, // spec §4.1
		IdleConnTimeout:       90 * time.Second,
	}
	return p
}

// hostname strips any port from the Host header and lowercases it.
func hostname(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}
