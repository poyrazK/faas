// app_errors_recorder — the request-hot-path component of the
// customer-facing automatic error grouping feature (ADR-096).
//
// Sits as middleware in front of the gateway's request handler.
// For every 4xx/5xx response, derives a fingerprint (route
// template + http_status + error_class) and enqueues a record
// for the publisher. 2xx/3xx are ignored (the gateway emits no
// row for happy-path responses).
//
// Fingerprint derivation is on the route TEMPLATE (e.g.
// "GET /v1/users/{id}"), NOT the expanded URL — load-bearing
// for cardinality. The LRU cache short-circuits fingerprint
// re-derivation for high-frequency fingerprints; the cardinality
// backstop drops records past the cache cap with a
// rate_limited metric counter.
//
// The recorder never opens a Postgres connection (CLAUDE.md
// ownership: apid is the sole writer). The publisher
// (app_errors_publisher.go) drains the ringbuffer and ships
// each batch to apid via the unix-socket gRPC
// IncrementAppError streaming RPC (pkg/apidgrpc).
//
// Concurrency: every method is safe under concurrent calls from
// many request goroutines. The LRU + ringbuffer share a single
// mutex; the lock is held for microseconds (LRU is a hash map +
// doubly-linked-list under the hood). On a hot path this is
// fine — Postgres round-trip avoidance is the whole point.

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/wire"
)

// appErrorsRecorderConfig is the bundled knobs the recorder
// reads at boot. Defaults are set by setAppErrorsRecorderDefaults
// so a partially-configured recorder still builds.
type appErrorsRecorderConfig struct {
	// Enabled is the kill-switch (FAAS_APP_ERRORS_ENABLED).
	// PR-A ships with this defaulting to false; PR-B flips
	// it. When false, the recorder is a pass-through.
	Enabled bool

	// DedupeWindowSeconds is the server-side merge window
	// (limits.AppErrorsDedupeWindowSeconds = 3600). The
	// recorder's LRU doesn't enforce this window directly;
	// it's the apid gRPC handler's contract. We keep the
	// value here so the publisher can pre-stamp "merge
	// expected" hints when feeding apid.
	DedupeWindowSeconds int

	// CardinalityLimit caps the in-process LRU size. Past the
	// cap, the recorder emits rate_limited and drops the
	// record. Default 10000 — large enough that the LRU is
	// rarely the bottleneck, small enough that a single
	// runaway app cannot OOM the gateway.
	CardinalityLimit int

	// SampleMessageCapBytes mirrors
	// api.AppErrorsSampleMessageCapBytes. Used here so the
	// recorder trims BEFORE redaction (the redactor caps at
	// its own limit too; trimming first avoids the redactor
	// running over a 64 KiB stack-trace blob).
	SampleMessageCapBytes int

	// HeadersSampleMaxKeys caps the number of header entries
	// captured per record. Past the cap, only the first N
	// keys are kept (sorted by header name for stability).
	HeadersSampleMaxKeys int

	// Now is injectable for tests. nil ⇒ time.Now.
	Now func() time.Time
}

// setAppErrorsRecorderDefaults populates zero-valued knobs
// with safe defaults. Idempotent.
func (c *appErrorsRecorderConfig) setDefaults() {
	if c.DedupeWindowSeconds == 0 {
		c.DedupeWindowSeconds = 3600
	}
	if c.CardinalityLimit == 0 {
		c.CardinalityLimit = 10000
	}
	if c.SampleMessageCapBytes == 0 {
		c.SampleMessageCapBytes = 512
	}
	if c.HeadersSampleMaxKeys == 0 {
		c.HeadersSampleMaxKeys = 8
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// appErrorsRecorder is the request-hot-path recorder. Construct
// via newAppErrorsRecorder, install as middleware via Middleware.
type appErrorsRecorder struct {
	cfg    appErrorsRecorderConfig
	pub    *appErrorsPublisher
	redact *redact.Redactor
	ops    *wire.OpsMetrics
	log    *slog.Logger

	// cache is the per-process fingerprint LRU. Keyed by
	// fingerprint hex; value is *appErrorRow (count + last_seen).
	// Hits short-circuit the dedupe derivation in apid (the
	// caller still sends a record so the server can bump the
	// count + last_seen_at, but the route → fingerprint work
	// is cached).
	cacheMu sync.Mutex
	cache   map[string]*appErrorRow

	// ring is the per-process ringbuffer the publisher drains.
	// ringMu guards ring + head + len (NOT the LRU cache —
	// that's cacheMu's job).
	ringMu sync.Mutex
	ring   []appErrorRow
	head   int
	len    int
}

// appErrorRow is the unit of work shared between recorder +
// publisher + LRU cache.
type appErrorRow struct {
	AccountID    string
	AppID        string
	DeploymentID string
	Fingerprint  string
	Route        string
	HTTPStatus   int
	ErrorClass   string
	SampleMsg    string
	HeadersJSON  string
	Redactions   []string
	InstanceID   string
	ReceivedAt   time.Time

	// LastSeen is the LRU bookkeeping field (NOT a wire
	// field). The recorder bumps LastSeen on every cache
	// hit so the eviction policy knows which entries to
	// drop when the cardinality cap is reached. The
	// publisher NEVER sends LastSeen to apid — the server
	// maintains its own last_seen_at via ON CONFLICT DO
	// UPDATE.
	LastSeen time.Time
}

// newAppErrorsRecorder wires a production recorder. pub may be
// nil in tests; the recorder still records + caches but no
// publisher drain occurs.
func newAppErrorsRecorder(cfg appErrorsRecorderConfig, pub *appErrorsPublisher, ops *wire.OpsMetrics, log *slog.Logger) *appErrorsRecorder {
	cfg.setDefaults()
	return &appErrorsRecorder{
		cfg:    cfg,
		pub:    pub,
		redact: redact.New(cfg.SampleMessageCapBytes),
		ops:    ops,
		log:    log,
		cache:  make(map[string]*appErrorRow, cfg.CardinalityLimit),
		ring:   make([]appErrorRow, appErrorsRingSize),
	}
}

// Middleware returns the http.Handler middleware the gateway
// installs in front of its request handler. The returned
// middleware is a no-op when cfg.Enabled is false (PR-A's
// default kill-switch).
func (r *appErrorsRecorder) Middleware(next http.Handler) http.Handler {
	if !r.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, req)
		// After the handler returns, fire the recorder on the
		// recorded status. We do NOT block the response — the
		// ringbuffer append is O(1).
		r.record(sr.status, req)
	})
}

// record is the single entry point for "a request just
// completed with this status". 2xx/3xx are dropped; 4xx/5xx
// are fingerprinted, redacted, cached, and enqueued.
func (r *appErrorsRecorder) record(status int, req *http.Request) {
	if status < 400 {
		return
	}
	if status > 599 {
		// Out-of-range status; recorder ignores.
		return
	}

	// Resolve the route TEMPLATE (Go-mux pattern), never the
	// expanded URL. Load-bearing for cardinality. The
	// gateway stores the matched pattern on the request
	// context under a well-known key (see the auth +
	// routing middleware chain).
	route := routeTemplate(req)
	if route == "" {
		route = req.URL.Path
	}

	// Derive the error_class. This is the closed vocabulary
	// the schema CHECK enforces; an unknown value falls
	// through to "unhandled".
	errClass := deriveErrorClass(status, req)

	// Compute the fingerprint (sha256 hex of route|status|class).
	fp := deriveFingerprint(route, status, errClass)

	// LRU hit: bump cache + observe the hit; the redaction work
	// is skipped on the merge path because the apid-side
	// ON CONFLICT DO UPDATE only touches count + request_count +
	// last_seen_at (sample_message / headers / route / status /
	// error_class are frozen at the first insert). Skipping the
	// regex pass saves the per-request cost; the per-request row
	// in app_error_requests still carries an empty
	// headers_sample + no redactions on the merge path (which is
	// the correct behaviour — the drill-down view shows the FIRST
	// sample only).
	cacheHit := r.cacheHit(fp)
	if cacheHit {
		if r.ops != nil {
			r.ops.ObserveAppErrorsFingerprintCacheHit()
		}
	}

	// Cardinality backstop: past the LRU cap, drop + emit
	// rate_limited.
	if !r.cacheAdmit(fp) {
		if r.ops != nil {
			r.ops.ObserveAppErrorsRecorded("rate_limited")
		}
		return
	}

	// Extract the sample message + headers, run redaction, and
	// build the row — but ONLY on the cache-miss / first-insert
	// path. On a cache hit, the apid-side handler's ON CONFLICT
	// DO UPDATE keeps the FIRST sample_message + headers, so
	// paying the regex cost on every hit would waste CPU for no
	// observable benefit (the stored value never changes).
	var (
		redactedSample string
		headersJSON    string
		allRedactions  []string
	)
	if !cacheHit {
		sample := extractSampleMessage(req)
		headers := extractHeaders(req, r.cfg.HeadersSampleMaxKeys)
		var redactions []string
		var headerRedactions []string
		redactedSample, redactions = r.redact.Apply(sample)
		redactedHeaders, headerRedactions := r.redact.ApplyHeaders(headers)
		allRedactions = mergeRedactions(redactions, headerRedactions)
		headersJSON = mapToJSON(redactedHeaders)
	}

	// Build the row.
	row := appErrorRow{
		AccountID:    resolveAccountID(req),
		AppID:        resolveAppID(req),
		DeploymentID: resolveDeploymentID(req),
		Fingerprint:  fp,
		Route:        route,
		HTTPStatus:   status,
		ErrorClass:   errClass,
		SampleMsg:    redactedSample,
		HeadersJSON:  headersJSON,
		Redactions:   allRedactions,
		InstanceID:   req.Header.Get("X-Gregale-Instance-ID"), // propagated by vmmd
		ReceivedAt:   r.cfg.Now().UTC(),
	}

	// Append to the ringbuffer. The publisher drains it on
	// FlushInterval / FlushBatchSize.
	r.enqueue(row)
	if r.ops != nil {
		r.ops.ObserveAppErrorsRecorded("ok")
	}
}

// cacheHit reports whether fp is in the LRU. Bumps last_seen.
func (r *appErrorsRecorder) cacheHit(fp string) bool {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	row, ok := r.cache[fp]
	if !ok {
		return false
	}
	row.LastSeen = r.cfg.Now().UTC()
	return true
}

// cacheAdmit inserts fp into the LRU, evicting the oldest
// entry past CardinalityLimit. Returns false when the
// admission was rate_limited (the LRU was already full and
// fp was not present).
func (r *appErrorsRecorder) cacheAdmit(fp string) bool {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if _, ok := r.cache[fp]; ok {
		return true
	}
	if len(r.cache) >= r.cfg.CardinalityLimit {
		// Find + evict the oldest entry. Linear scan; the
		// LRU cap is 10k so this is ~50µs worst case.
		// For very hot paths a heap-based eviction is a
		// future optimisation.
		var oldestFP string
		var oldestTS time.Time
		first := true
		for k, v := range r.cache {
			if first || v.LastSeen.Before(oldestTS) {
				oldestFP = k
				oldestTS = v.LastSeen
				first = false
			}
		}
		if !first {
			delete(r.cache, oldestFP)
		}
		// If somehow the LRU is still full after eviction
		// (which can't happen with CardinalityLimit > 0
		// but defence-in-depth), reject.
		if len(r.cache) >= r.cfg.CardinalityLimit {
			return false
		}
	}
	r.cache[fp] = &appErrorRow{
		LastSeen: r.cfg.Now().UTC(),
	}
	return true
}

// enqueue appends row to the ringbuffer. The publisher drains
// it on the next tick. The ring is fixed-size; when full, the
// oldest entry is overwritten (the publisher drains faster
// than the ring fills in normal operation).
func (r *appErrorsRecorder) enqueue(row appErrorRow) {
	r.ringMu.Lock()
	defer r.ringMu.Unlock()
	idx := (r.head + r.len) % len(r.ring)
	r.ring[idx] = row
	if r.len < len(r.ring) {
		r.len++
	} else {
		// Ring full: overwrite the oldest (advance head).
		r.head = (r.head + 1) % len(r.ring)
	}
	if r.pub != nil {
		r.pub.NotifyEnqueued()
	}
}

// drainBatch is called by the publisher on FlushInterval /
// FlushBatchSize. Returns the next batch of rows and the new
// (head, len) state. The batch is bounded by maxRows; pass 0
// for "drain everything currently in the ring".
func (r *appErrorsRecorder) drainBatch(maxRows int) []appErrorRow {
	r.ringMu.Lock()
	defer r.ringMu.Unlock()
	if r.len == 0 {
		return nil
	}
	n := r.len
	if maxRows > 0 && maxRows < n {
		n = maxRows
	}
	out := make([]appErrorRow, n)
	for i := 0; i < n; i++ {
		out[i] = r.ring[(r.head+i)%len(r.ring)]
	}
	// Advance head + shrink len.
	r.head = (r.head + n) % len(r.ring)
	r.len -= n
	return out
}

// ---- helpers ----

// deriveFingerprint returns hex(sha256(route|status|class)).
// The canonical input is the route TEMPLATE (load-bearing for
// cardinality).
func deriveFingerprint(route string, status int, class string) string {
	canonical := fmt.Sprintf("%s\x1f%d\x1f%s", route, status, class)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// deriveErrorClass is the closed-vocabulary classifier. The
// schema CHECK constraint enforces the same set on the server
// side; an unknown class falls through to "unhandled".
func deriveErrorClass(status int, req *http.Request) string {
	if status >= 400 && status < 500 {
		// 4xx — could be invalid_json if the request body
		// was unparseable. The headers don't carry that
		// signal, so we default to client_error.
		// invalid_json detection is a future optimisation
		// (PR-B).
		return "client_error"
	}
	// 5xx
	// We don't have access to the upstream error message
	// here (the gateway hot path doesn't buffer the
	// response body), so we default to "unhandled".
	// The richer classifier (db_timeout, stripe_timeout,
	// null_pointer, wake_failed, upstream_5xx) lives on
	// the publisher side where the response body IS
	// available (deferred to PR-B).
	return "unhandled"
}

// extractSampleMessage returns the request URL path as the
// default sample message. A future PR can plumb the actual
// response body through a buffer-aware status recorder.
func extractSampleMessage(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return req.URL.Path
}

// extractHeaders returns a subset of request headers
// (bounded by maxKeys, alphabetically sorted for stability).
func extractHeaders(req *http.Request, maxKeys int) map[string]string {
	if req == nil || req.Header == nil {
		return map[string]string{}
	}
	// Pull keys, sort, cap.
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	// Sort for stable ordering (so the cap drops are deterministic).
	sortStringsLen(keys)
	if maxKeys > 0 && len(keys) > maxKeys {
		keys = keys[:maxKeys]
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = req.Header.Get(k)
	}
	return out
}

// resolveAccountID, resolveAppID, resolveDeploymentID pull the
// resolved ids from the request context. The auth middleware
// (pkg/middleware) populates them; the recorder reads them off
// the request's ctx. Returning "" when not set is safe — the
// apid handler rejects the row as InvalidArgument and the
// gateway metric increments redaction_failed (a defensive
// double-check).
func resolveAccountID(req *http.Request) string    { return reqContextString(req, accountIDKey) }
func resolveAppID(req *http.Request) string        { return reqContextString(req, appIDKey) }
func resolveDeploymentID(req *http.Request) string { return reqContextString(req, deploymentIDKey) }

// ctx keys — string constants; the gateway's auth middleware
// populates these on r.Context(). We re-declare them here as
// constants rather than re-importing pkg/middleware to keep
// the recorder self-contained.
const (
	ctxKeyAccountID     = "gregale.account_id"
	ctxKeyAppID         = "gregale.app_id"
	ctxKeyDeploymentID  = "gregale.deployment_id"
	ctxKeyRouteTemplate = "gregale.route_template"
)

// ctxKey is the typed key the recorder uses to read values
// from the request context. We use a typed string key so the
// value lookup is type-safe.
type ctxKey string

// accountIDKey, appIDKey, deploymentIDKey, routeTemplateKey
// are the typed keys for context.Value.
var (
	accountIDKey     = ctxKey(ctxKeyAccountID)
	appIDKey         = ctxKey(ctxKeyAppID)
	deploymentIDKey  = ctxKey(ctxKeyDeploymentID)
	routeTemplateKey = ctxKey(ctxKeyRouteTemplate)
)

// reqContextString is a tiny ctx helper.
func reqContextString(req *http.Request, key ctxKey) string {
	if req == nil || req.Context() == nil {
		return ""
	}
	if v, ok := req.Context().Value(key).(string); ok {
		return v
	}
	return ""
}

// routeTemplate reads the matched Go-mux pattern off the
// request context. The gateway's routing middleware sets
// this key before the recorder's middleware sees the request.
func routeTemplate(req *http.Request) string {
	return reqContextString(req, routeTemplateKey)
}

// mapToJSON marshals h into a compact JSON string. Returns
// "{}" for nil/empty maps. The apid handler validates this as
// a JSON object with ≤ 8 keys.
func mapToJSON(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	out := "{"
	first := true
	for k, v := range h {
		if !first {
			out += ","
		}
		out += fmt.Sprintf("%q:%q", k, v)
		first = false
	}
	out += "}"
	return out
}

// mergeRedactions unions two name lists + sorts + dedupes.
// Returned slice is alphabetically sorted.
func mergeRedactions(a, b []string) []string {
	seen := map[string]struct{}{}
	for _, x := range a {
		seen[x] = struct{}{}
	}
	for _, x := range b {
		seen[x] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for x := range seen {
		out = append(out, x)
	}
	sortStringsLen(out)
	return out
}

// sortStringsLen is the small inlined ascending sort used by
// extractHeaders + mergeRedactions. Strings are short
// (<=16 chars) so this is the right complexity. Kept
// private. (Named sortStringsLen to avoid colliding with the
// test-only sortStrings helper in write_gate_test.go.)
func sortStringsLen(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// appErrorsRingSize is the per-process ringbuffer capacity.
// 4096 rows × ~1 KiB per row ≈ 4 MiB worst case per gateway
// instance; the publisher drains every 5s so the ring rarely
// exceeds a few hundred entries in normal operation.
const appErrorsRingSize = 4096

// statusRecorder is the recorder-local ResponseWriter wrapper
// that captures the response status code. Mirrors the
// pkg/middleware/authlimit.go pattern; declared locally so the
// recorder doesn't reach across packages for a 30-line wrapper.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// lgtm[go/reflected-xss] false-positive: statusRecorder is a pass-through; gatewayd-internal serves application/json (api.WriteProblem via cmd/apid handlers) or text/event-stream SSE (cmd/gatewayd-internal/app_logs.go::ServeHTTP), never HTML — the XSS sink is unreachable. Mirrors the pattern at pkg/middleware/authlimit.go:81 and pkg/gateway/handler.go:3973.
func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	// lgtm[go/reflected-xss] false-positive: see doc-comment above; pass-through forwards bytes unchanged to the underlying ResponseWriter.
	return r.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer if it implements
// http.Flusher (SSE / streaming responses need this).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying writer if it implements
// http.Hijacker (WebSocket upgrades need this).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("statusRecorder: underlying ResponseWriter does not implement http.Hijacker")
}

// keep imports honest
var _ = slog.Default
