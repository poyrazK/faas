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

// Target is one routable instance in the gateway's per-app cache (issue
// #168). Multiple Targets per app = real fan-out across max_concurrency.
// The NodeID is the compute_node.id the instance lives on (ADR-028); the
// forwarder dereferences it via the per-node vmmd client cache. The
// InstanceID is the instances.id row schedd owns — used to attribute
// last_request_at touches (spec §4.1) and to stamp x-faas-instance on
// the request before proxying.
type Target struct {
	NodeID     string
	InstanceID string
	WakeID     string
	AddedAt    time.Time
}

// Backend is the seam between the edge and the rest of the platform (in
// production: the routing cache over Postgres, and schedd over gRPC). Splitting
// it out keeps the hot request path testable end-to-end without a real cluster.
//
// Issue #168 widened this interface to support per-app fan-out:
//   - Pick returns one routable instance for the app (round-robin across
//     max_concurrency), used on every request (cold or warm).
//   - HealthyCount returns the number of routable instances currently cached
//     for the app. Drives the WakeGate's shouldWake predicate: stop admitting
//     once we're at the plan's effective max_concurrency.
//   - Admit asks schedd to admit ONE additional instance for the app, gated
//     by maxConcurrency so concurrent callers cannot collectively over-admit
//     past the cap (issue #168 trust model). Returns the new Target's
//     WakeID on the admitted path, atCapacity=true when the cache is
//     already at maxConcurrency (the gateway treats this as a benign
//     no-op when it has ≥1 cached target), or an *api.Problem on real
//     failure (RAM headroom, chooser, store).
type Backend interface {
	// Lookup resolves a hostname to its app (cache-first, spec §4.1).
	Lookup(ctx context.Context, host string) (App, bool)
	// Pick returns one routable Target for appID via atomic round-robin, or
	// ok=false when the cache is empty (caller should ensure capacity first).
	Pick(appID string) (Target, bool)
	// HealthyCount returns the number of routable Targets currently cached
	// for appID. Drives the WakeGate's shouldWake predicate.
	HealthyCount(appID string) int
	// Admit asks schedd to admit ONE additional instance for appID, only
	// when HealthyCount(appID) < maxConcurrency at the moment the call
	// commits. Implementations MUST serialize the HealthyCount check and
	// the cache update so a burst of concurrent Admit calls can never
	// collectively exceed maxConcurrency (issue #168 fan-out invariant).
	// On the admitted path wakeID is non-empty and the new Target is
	// cached. On the at-capacity path wakeID is empty and err is nil.
	// On real failure err is a non-nil *api.Problem.
	Admit(ctx context.Context, appID string, maxConcurrency int) (wakeID string, atCapacity bool, err error)
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
	// proxyByNode builds the reverse proxy for a compute_node.id (issue
	// #98 / ADR-028). When non-nil, the handler dispatches every
	// request through it instead of proxyFor — the string returned by
	// Backend.Pick is interpreted as a node id and dereferenced via
	// the per-node vmmd client cache. nil = legacy addr-based path
	// (default for tests and the e2e harness; production wires
	// ForwardingReverseProxy in cmd/gatewayd/main.go).
	proxyByNode func(nodeID string) http.Handler
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

// WithLimiter installs the per-app rate limiter. Production wires the
// token-bucket Limiter; load tests install an unlimitedLimiter so they
// aren't constrained by the plan rps/burst from newTestHandler's
// PlanPro default (which would 429 ~half of a 1k rps test). Treat this
// as a test-only seam; do NOT expose it as a config knob.
func (h *Handler) WithLimiter(l *Limiter) *Handler {
	h.limiter = l
	return h
}

// WithForwarding installs the per-node HTTP→gRPC forwarder built by
// pkg/gateway/forwardproxy.go (issue #98 / ADR-028). When set, every
// request dispatches through fn(nodeID) where nodeID is the value
// Backend.Pick returned. nil-safe: pass nil to revert to the legacy
// addr-based proxy path (used by tests and the e2e harness).
func (h *Handler) WithForwarding(fn func(nodeID string) http.Handler) *Handler {
	h.proxyByNode = fn
	return h
}

// SetWakeGateHook installs a callback that wakes the queue-depth gauge each
// time WakeGate mutates an entry, and hands the wake-queue histogram to the
// gate so Wait can observe per-caller wait duration. Called by main once the
// metrics bundle exists.
func (h *Handler) SetWakeGateHook() {
	h.gate.onChange = func(appID string, depth int) {
		if h.metrics != nil {
			h.metrics.SetQueueDepth(appID, depth)
		}
	}
	h.gate.SetMetrics(h.metrics)
}

// Metrics exposes the Prometheus bundle (used by the control listener to mount
// /metrics). May be nil if NewHandler was used and nothing initialized one.
func (h *Handler) Metrics() *Metrics { return h.metrics }

// Limiter exposes the per-app rate limiter so callers (SIGHUP handler,
// admin endpoints) can forget buckets. Mostly an M5 hook: when apid starts
// pushing plan changes via Postgres + LISTEN, the gateway's reload path
// calls ForgetAll() on this limiter.
func (h *Handler) Limiter() *Limiter { return h.limiter }

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
		h.observe(r, rec.status, "", "", false, Target{})
		return
	}

	app, ok := h.backend.Lookup(r.Context(), host) //nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No such app", fmt.Sprintf("no app is routed to %q", host)))
		h.observe(r, rec.status, "", "", false, Target{})
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
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// Cap request body either direction (spec §4.1).
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxRequestBodyBytes)

	// Per-app fan-out admission (issue #168). The WakeGate's
	// shouldWake predicate runs HealthyCount against the plan's
	// effective max_concurrency, so a burst of N requests admits up to
	// N instances before short-circuiting.
	limits, _ := api.LimitsFor(app.Plan)
	//nolint:contextcheck // request ctx at handler boundary.
	cold, wakeID, err := h.ensureCapacity(r.Context(), app.ID, limits.MaxConcurrency)
	if err != nil {
		writeWakeError(w, err)
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// Pick one routable Target via atomic round-robin. After a
	// successful ensure, HealthyCount ≥ 1, so this should succeed
	// unless every cached instance was evicted between admit and pick
	// (an instance_changed notification race). On that rare miss, fall
	// through to the capacity problem — the WakeGate will retry on the
	// next request.
	target, ok := h.backend.Pick(app.ID)
	if !ok {
		// Race: every cached instance was evicted between
		// ensureCapacity returning and our Pick. Surface the observed
		// (current) HealthyCount so the operator's metrics panel
		// shows 0 vs the cap (was 1+ microseconds ago).
		writeWakeError(w, api.ErrAppConcurrencyReached(limits, h.backend.HealthyCount(app.ID)))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// Stamp the per-instance identity on the request BEFORE proxying so
	// the per-node vmmd forwarder (issue #98 / ADR-028) can attribute
	// the HTTP bytes to this exact instance. Overwrites any inbound
	// x-faas-instance so an attacker can't steer the proxy to an
	// arbitrary instance by setting the header (issue #168 trust model).
	r.Header.Set("x-faas-instance", target.InstanceID)

	// Per-request wake-timing recorder (spec §6.3) installed AFTER
	// upstream stamping so the trace sees only the proxy hop, not the
	// stamping overhead.
	firstByteRec := &firstByteRecorder{}
	//nolint:contextcheck // WithFirstByteRecorder wraps context.WithValue on r.Context(); lint can't trace through the function call.
	r = r.WithContext(WithFirstByteRecorder(r.Context(), firstByteRec))

	if cold {
		// Cold-wake transparency (UX spec §6): let developers see the penalty.
		w.Header().Set("x-faas-wake", "cold")
	}
	if wakeID != "" {
		// Per-wake correlation handle (gaps analysis 2026-07-23). Sits
		// next to x-faas-wake so the two are co-located on every
		// response, and never set on a Phase-1 fast-path response.
		w.Header().Set("x-faas-wake-id", wakeID)
	}

	wakeStart := time.Now()
	if h.proxyByNode != nil {
		// Issue #98 / ADR-028: Target.NodeID is the compute_node.id;
		// the forwarder dials the per-node vmmd over the overlay and
		// bridges the HTTP bytes through the instance netns via the
		// ForwardHTTP RPC. target stays in scope for the metrics
		// labels and observe() last-seen hook below.
		h.proxyByNode(target.NodeID).ServeHTTP(w, r)
	} else {
		// Legacy addr-based path. Target.NodeID is treated as a
		// host:port by defaultProxy — preserved for tests and the
		// e2e harness without a vmmd overlay.
		h.proxyFor(target.NodeID).ServeHTTP(w, r)
	}
	h.observe(r, rec.status, app.ID, string(app.Plan), cold, target)
	if cold && h.metrics != nil {
		// Wake latency is "request-received to first upstream byte". The
		// wake-timing RoundTripper stamps the inbound request's recorder at
		// GotFirstResponseByte; reading it back here yields the actual wake
		// slice, not "wake + full upstream body copy" (the prior proxy return
		// measurement). On any path where the stamp never landed (proxy error
		// before headers), we fall back to the full duration with a Warn so
		// the gap is observable but the dashboard still gets a sample.
		firstByteAt, ok := FirstByteFrom(r)
		if !ok {
			h.log.Warn("gateway: wake-timing first-byte stamp missing; observing full proxy duration",
				"app", app.ID, "node", target.NodeID, "instance", target.InstanceID)
			firstByteAt = time.Now()
		}
		h.metrics.ObserveColdWake(app.ID, firstByteAt.Sub(wakeStart))
	}
}

// observe emits one metric increment + one structured log line per request.
// Always called exactly once on the ServeHTTP exit path; missing it would
// skew the §12 dashboard. On a 2xx response it also Touches the LastSeenSink
// keyed by InstanceID (issue #168 — per-instance attribution survives the
// multi-instance fan-out where multiple instances share a single node).
func (h *Handler) observe(r *http.Request, status int, appID, plan string, cold bool, target Target) {
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
	if h.lastSeen != nil && status >= 200 && status < 300 && target.InstanceID != "" {
		h.lastSeen.Touch(target.InstanceID, time.Now())
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

// ensureCapacity (issue #168) is the per-app fan-out admission primitive.
//
// Three paths:
//
//  1. Cold start (HealthyCount == 0): go through the WakeGate so a
//     burst of N concurrent cold requests to a fully-parked app
//     coalesces to ONE cold boot per "generation". The leader runs
//     ensure(); followers wait on its result, then EACH re-enters the
//     cold-start loop and admits its own instance IF HealthyCount is
//     still < max_concurrency. This is the per-generation fan-out: a
//     burst of N requests against a parked app admits up to
//     max_concurrency distinct instances, where 1 <= admitted <= N.
//     The loop is bounded by max_concurrency so a single request
//     cannot drive past the cap by itself (the cap is enforced per
//     request, not per generation).
//
//  2. Fan-out (HealthyCount > 0, < max_concurrency): skip the gate and
//     call Admit directly. Sequential requests after the cold-start
//     burst go through this path; schedd's own ledger enforces the cap
//     atomically.
//
//  3. Saturated (HealthyCount >= max_concurrency): no-op. Pick returns
//     one of the cached targets.
//
// Returns (cold, wakeID, err):
//   - cold=true on a fresh admit (one or more new instances reached RUNNING);
//     cold=false when the request hit an existing cached target with no
//     fresh admit fired.
//   - wakeID is non-empty on a fresh admit, empty when no admit fired.
//   - err is non-nil only on real admission failures (RAM headroom, chooser,
//     store). The benign app_concurrency_reached outcome is never lifted to
//     an error by Backend.Admit.
func (h *Handler) ensureCapacity(ctx context.Context, appID string, maxConcurrency int) (cold bool, wakeID string, err error) {
	// Loop bound: a single request can drive at most max_concurrency
	// iterations (cold-start with follow-up fan-out). The cap is
	// enforced atomically by Backend.Admit (HealthyCount + add as one
	// serialized op), so this loop is bounded by observation, not by
	// speculation about concurrency.
	for attempt := 0; attempt < maxConcurrency; attempt++ {
		healthy := h.backend.HealthyCount(appID)
		if healthy == 0 {
			c, w, e := h.coldStart(ctx, appID, maxConcurrency)
			if e != nil {
				return false, "", e
			}
			if c {
				return true, w, nil
			}
			// Cold-start saw no need to admit (a peer's wake
			// already populated the cache). Re-check HealthyCount
			// and fall through to fan-out / saturation on the next
			// iteration.
			continue
		}
		if healthy >= maxConcurrency {
			return false, "", nil
		}
		// Fan-out path: admit directly, no gate. Backend.Admit
		// atomically checks HealthyCount < maxConcurrency under its
		// own lock, so concurrent callers cannot collectively
		// exceed the cap.
		wakeID, atCapacity, e := h.backend.Admit(ctx, appID, maxConcurrency)
		if e != nil {
			return false, "", e
		}
		if atCapacity {
			return false, "", nil
		}
		return true, wakeID, nil
	}
	return false, "", nil
}

// coldStart is path 1 of ensureCapacity: HealthyCount == 0, so we go
// through the WakeGate's single-flight coalescing. shouldWake is held
// under the gate lock and re-runs HealthyCount; if a peer's admit has
// just landed, we skip the redundant cold boot.
func (h *Handler) coldStart(ctx context.Context, appID string, maxConcurrency int) (bool, string, error) {
	var admittedWakeID string
	var cold bool
	werr := h.gate.Wait(ctx, appID,
		func() bool {
			return h.backend.HealthyCount(appID) < maxConcurrency
		},
		func(ctx context.Context) error {
			id, atCapacity, e := h.backend.Admit(ctx, appID, maxConcurrency)
			if e != nil {
				return e
			}
			if atCapacity {
				return nil
			}
			admittedWakeID = id
			cold = true
			return nil
		},
	)
	if werr != nil {
		return false, "", werr
	}
	return cold, admittedWakeID, nil
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

// sharedUpstreamTransport is the single *http.Transport gatewayd uses to
// proxy to all upstream microVMs. It is wrapped in a firstByteRoundTripper
// so the wake-timing trace can stamp the inbound request's recorder at
// "first upstream response byte" (spec §6.3, §12). Sharing one transport
// across requests matches Go's stdlib expectation (connection pooling
// requires a single transport per upstream) and the spec's "single public
// listener" invariant — gatewayd owns this transport exclusively.
var sharedUpstreamTransport = newFirstByteRoundTripper(&http.Transport{
	ResponseHeaderTimeout: 60 * time.Second, // spec §4.1
	IdleConnTimeout:       90 * time.Second,
})

// defaultProxy returns a reverse proxy to addr (spec §4.1: 60 s to first
// response byte). The spec's "25 MB either direction" outbound cap is enforced
// by Server.MaxResponseBodyBytes on the http.Server wrapping this handler, so
// it doesn't need to live inside the proxy itself.
func defaultProxy(addr string) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = sharedUpstreamTransport
	return p
}

// hostname strips any port from the Host header and lowercases it.
func hostname(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}
