package middleware

import (
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
func AuthLimit(cfg AuthLimitConfig) func(http.Handler) http.Handler {
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
	// Default CountStatuses = [401]; the sentinel form [0] (CountEveryAttempt)
	// counts every response. nil also means "default 401" so existing callers
	// don't have to repeat it.
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
	l := &authLimiter{cfg: cfg}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := cfg.ClientIPFn(r)
			if l.isLimited(ip, cfg.Now()) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many failed login attempts; try again in 60 seconds", http.StatusTooManyRequests)
				cfg.Log.Warn("auth_limit blocked",
					"ip", logsanitize.Field(ip),
					"path", logsanitize.Field(r.URL.Path),
					"request_id", RequestIDFrom(r),
				)
				return
			}
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			if countFn(rw.status) {
				l.recordFailure(ip, cfg.Now())
			}
		})
	}
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
func defaultClientIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may already be a bare IP (rare); accept it.
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
