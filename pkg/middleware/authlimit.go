package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// AuthLimitConfig is the per-IP rate limit on failed auth attempts from a
// protected handler (spec §11: "rate limit auth failures (10/min/IP)").
// Default values match the spec; tests can override.
//
// Keying is by client IP alone — do not fragment to per-endpoint or
// per-tenant. Spec §11 says "10/min/IP" — the doc-comment on the wire
// field makes the binding explicit so a future maintainer doesn't add
// a parallel limiter.
type AuthLimitConfig struct {
	// Window is the sliding-window length. Default 1m.
	Window time.Duration
	// MaxFailures is the threshold inside the window. Default 10.
	MaxFailures int
	// Now is injectable for tests. nil ⇒ time.Now.
	Now func() time.Time
	// Log is the structured logger for the 429 event. nil ⇒ slog.Default.
	Log *slog.Logger
	// ClientIPFn extracts the rate-limit key. nil ⇒ net.SplitHostPort
	// from r.RemoteAddr, falling back to the literal string.
	ClientIPFn func(*http.Request) string
	// CountStatuses lists the HTTP statuses that count toward the
	// per-IP bucket. Default: [401]. The sentinel value 0 means
	// "count every response, regardless of status" — used on /login
	// where anti-enumeration returns 200 even for unknown emails, so
	// a true 401/403-only limiter would miss the brute-force signal.
	CountStatuses []int
}

// CountEveryAttempt is the sentinel status for CountStatuses meaning
// "count every response regardless of status". See AuthLimitConfig.
const CountEveryAttempt = 0

// AuthLimit wraps next so that after MaxFailures tracked responses from
// a single client IP inside the Window, subsequent requests from that IP
// get 429 Retry-After=60 with no further handler work. The limiter is
// in-memory; this is a defence-in-depth layer over the gateway's
// edge-level per-app limiter, not a multi-host accurate counter.
//
// The bucket is fresh per call. To share one bucket across multiple
// routes (so spec §11 "10/min/IP" is enforced across the entire /v1/*
// surface, not per route), use NewLimiter + AuthLimitWithLimiter.
func AuthLimit(cfg AuthLimitConfig) func(http.Handler) http.Handler {
	return AuthLimitWithLimiter(cfg, NewLimiter(cfg))
}

// statusRecorder is a tiny ResponseWriter wrapper that captures the
// status code so AuthLimit can react to 401s without buffering the
// body. Forwarding Flush/Hijack is required: SSE handlers (e.g. apid's
// streamDeploymentLogs) type-assert http.Flusher and panic if the
// wrapper doesn't expose it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(p)
}

// Flush forwards to the underlying ResponseWriter if it implements
// http.Flusher. Returns false otherwise (which is fine — non-flushable
// writers like the test recorder still work, they just don't stream).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying ResponseWriter if it implements
// http.Hijacker (WebSocket upgrade, raw TCP behind the writer).
// Returning http.ErrNotSupported keeps net/http's contract: a handler
// that requests Hijack on a non-hijackable writer must fail loudly.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// authLimiter is the in-memory per-IP token bucket. failures is a
// sorted slice of timestamps; on each new failure we drop anything
// older than Now()-Window and check the length against MaxFailures.
//
// mutex covers the whole map because the cost of contention is
// dwarfed by the response work — auth failures are rare, not hot.
type authLimiter struct {
	cfg AuthLimitConfig
	mu  sync.Mutex
	// failures is keyed by client IP. Each value is a slice of failure
	// timestamps in arrival order.
	failures map[string][]time.Time
}

// Limiter is the exported handle on an authLimiter. Use NewLimiter to
// share one bucket across multiple AuthLimit-wrapped handlers
// (spec §11 "rate limit auth failures 10/min/IP" is per-IP, not per
// (IP, endpoint) — callers MUST share a Limiter across every route
// they want covered by the same budget, otherwise each route gets its
// own 10/min budget and the spec is silently violated).
type Limiter struct{ inner *authLimiter }

// NewLimiter returns a fresh Limiter for cfg. Pass the same Limiter
// to AuthLimitWithLimiter on every handler that should share the
// budget.
func NewLimiter(cfg AuthLimitConfig) *Limiter {
	if cfg.Window == 0 {
		cfg.Window = time.Minute
	}
	if cfg.MaxFailures == 0 {
		cfg.MaxFailures = 10
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.ClientIPFn == nil {
		cfg.ClientIPFn = defaultClientIP
	}
	return &Limiter{inner: &authLimiter{cfg: cfg}}
}

// AuthLimitWithLimiter is AuthLimit but the bucket is shared with other
// handlers that pass the same Limiter. Use this on a server's entire
// /v1/* surface so a brute-force attack spread across routes still
// trips the per-IP budget. The cfg's Window/MaxFailures/Now/Log fields
// are read from limiter's underlying cfg; the cfg passed here is used
// only for CountStatuses / CountEveryAttempt semantics.
func AuthLimitWithLimiter(cfg AuthLimitConfig, lim *Limiter) func(http.Handler) http.Handler {
	if lim == nil {
		// Refuse to silently fall back to a fresh bucket: that is exactly
		// the spec-violating behaviour this constructor exists to prevent.
		panic("middleware: AuthLimitWithLimiter called with nil Limiter")
	}
	if cfg.Now == nil {
		// Default time source — NewLimiter already set this on lim.inner.cfg,
		// but the cfg the caller passes here drives cfg.Now() inside the
		// per-request closure, so it must also default independently.
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.ClientIPFn == nil {
		cfg.ClientIPFn = defaultClientIP
	}
	countAll := false
	for _, s := range cfg.CountStatuses {
		if s == CountEveryAttempt {
			countAll = true
			break
		}
	}
	if !countAll && len(cfg.CountStatuses) == 0 {
		cfg.CountStatuses = []int{http.StatusUnauthorized}
	}
	countFn := func(status int) bool {
		if countAll {
			return true
		}
		for _, s := range cfg.CountStatuses {
			if s == status {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := cfg.ClientIPFn(r)
			if lim.inner.isLimited(ip, cfg.Now()) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many failed login attempts; try again in 60 seconds", http.StatusTooManyRequests)
				cfg.Log.Warn("auth_limit blocked",
					"ip", logsanitize.Field(ip),
					"path", logsanitize.Field(r.URL.Path),
					// The id helper falls back to the X-Request-ID header set
					// by an upstream proxy (or directly by the caller); both
					// are attacker-influenced. Sanitize before logging so a
					// crafted id with a CR/LF can't smuggle an extra log line
					// (CWE-117, CodeQL go/log-injection).
					"request_id", logsanitize.Field(RequestIDFrom(r)),
				)
				return
			}
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			if countFn(rw.status) {
				lim.inner.recordFailure(ip, cfg.Now())
			}
		})
	}
}

func (l *authLimiter) recordFailure(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failures == nil {
		l.failures = make(map[string][]time.Time)
	}
	cutoff := now.Add(-l.cfg.Window)
	fs := l.failures[ip]
	// Drop expired entries from the front.
	i := 0
	for i < len(fs) && fs[i].Before(cutoff) {
		i++
	}
	fs = fs[i:]
	fs = append(fs, now)
	l.failures[ip] = fs
}

func (l *authLimiter) isLimited(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	fs, ok := l.failures[ip]
	if !ok {
		return false
	}
	cutoff := now.Add(-l.cfg.Window)
	i := 0
	for i < len(fs) && fs[i].Before(cutoff) {
		i++
	}
	fs = fs[i:]
	if len(fs) == 0 {
		delete(l.failures, ip)
		return false
	}
	l.failures[ip] = fs
	return len(fs) >= l.cfg.MaxFailures
}

// defaultClientIP returns the IP portion of r.RemoteAddr, falling back
// to the literal string when no host:port split is possible (unix
// sockets, tests).
//
// Issue #89: apid binds loopback-only (spec §11), so when requests
// reach apid via the gatewayd → apid loopback hop, r.RemoteAddr is
// 127.0.0.1:<ephemeral> for every customer and the per-IP bucket
// collapses to one. gatewayd pins X-Forwarded-For to the real client
// IP on every /v1/* forward (cmd/gatewayd/proxy.go::proxyToApid). We
// trust the header only when:
//
//   - r.RemoteAddr is loopback (127.0.0.0/8 or ::1) — the connection
//     came from this host, so the only way X-Forwarded-For is set
//     is by a trusted local proxy (gatewayd). A customer on the
//     public internet cannot reach a loopback hop directly.
//   - the header carries exactly one IP — gatewayd always sets one
//     value; a chain ("a, b") means an upstream proxy is already in
//     the path and we cannot trust any link in it.
//
// In every other case (no header, multi-hop chain, non-loopback hop,
// malformed value) we fall back to r.RemoteAddr's host, which is the
// safe default — the bucket may over-merge, but a customer can never
// reach a position where they can spoof the header to bypass it.
func defaultClientIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may already be a bare IP (rare); accept it.
		return strings.TrimSpace(r.RemoteAddr)
	}
	if isLoopbackHost(host) {
		// Loopback hop — try to recover the real client IP from
		// gatewayd's pin. Only trust the header when it carries
		// exactly one value (see function comment).
		if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" &&
			!strings.Contains(v, ",") {
			if ip := strings.TrimSpace(v); ip != "" && !isLoopbackHost(ip) &&
				net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	return host
}

// isLoopbackHost reports whether s is an IPv4 127.0.0.0/8 address or
// IPv6 ::1. Returns false on unparseable input (untrusted callers
// default to the RemoteAddr fallback above).
func isLoopbackHost(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
