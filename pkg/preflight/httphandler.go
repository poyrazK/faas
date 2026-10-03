package preflight

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// commitSHAPattern recognizes a fully pinned ref, which can be cache-checked
// before any upstream call because it names an immutable tree.
var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Handler serves the public preflight check. It is unauthenticated by design:
// the whole point is that somebody can find out whether their app would run
// here before deciding to trust us with anything.
type Handler struct {
	checker *Checker
	cache   *reportCache
	limiter *ipLimiter
}

// NewHandler wires a checker to the per-IP limiter and verdict cache.
func NewHandler(checker *Checker) *Handler {
	return &Handler{
		checker: checker,
		cache:   newReportCache(api.PreflightCacheMaxEntries, api.PreflightCacheTTL),
		limiter: newIPLimiter(api.PreflightRateLimitPerHour, time.Hour),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r), time.Now()) {
		writeProblem(w, http.StatusTooManyRequests, "preflight_rate_limited",
			"Too many checks", "Wait a few minutes before checking another repository.")
		return
	}
	source, err := ParseSource(r.URL.Query().Get("source"))
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, "preflight_invalid_source",
			"Not a GitHub repository", "Paste a public github.com repository URL, or owner/repo.")
		return
	}
	if ref := strings.TrimSpace(r.URL.Query().Get("ref")); ref != "" {
		if !validRef(ref) {
			writeProblem(w, http.StatusUnprocessableEntity, "preflight_invalid_source",
				"Invalid ref", "A ref must be a branch, tag, or commit SHA.")
			return
		}
		source.Ref = ref
	}
	if report, ok := h.cache.get(cacheKey(source.FullName(), source.Ref)); ok {
		writeReport(w, report)
		return
	}
	report, err := h.checker.Check(r.Context(), source)
	if err != nil {
		writeCheckProblem(w, err)
		return
	}
	h.cache.put(cacheKey(source.FullName(), source.Ref), report)
	h.cache.put(cacheKey(source.FullName(), report.CommitSHA), report)
	writeReport(w, report)
}

// validRef keeps a caller-supplied ref from steering the upstream URL. Refs
// are interpolated into a path, so slashes and relative elements are refused
// rather than escaped.
func validRef(ref string) bool {
	return len(ref) <= 100 && repoPattern.MatchString(ref) && strings.Trim(ref, ".") != ""
}

func cacheKey(fullName, ref string) string {
	if ref == "" {
		return ""
	}
	if !commitSHAPattern.MatchString(ref) {
		// A branch or tag moves, so only a pinned commit is safely cacheable
		// ahead of resolution.
		return ""
	}
	return fullName + "@" + ref
}

func writeReport(w http.ResponseWriter, report Report) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(report)
}

func writeProblem(w http.ResponseWriter, status int, code, title, detail string) {
	api.WriteProblem(w, api.NewProblem(status, code, title, detail))
}

func writeCheckProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRepoNotFound):
		writeProblem(w, http.StatusNotFound, "preflight_repo_not_found",
			"Repository not found", "The repository does not exist, or it is private. Preflight only reads public repositories.")
	case errors.Is(err, ErrSourceTooLarge):
		writeProblem(w, http.StatusUnprocessableEntity, "preflight_source_too_large",
			"Repository too large", "The source exceeds the size preflight will inspect.")
	case errors.Is(err, ErrUpstreamRateLimited):
		writeProblem(w, http.StatusServiceUnavailable, "preflight_upstream_unavailable",
			"GitHub is rate limiting us", "Try again shortly.")
	case errors.Is(err, ErrInvalidSource):
		writeProblem(w, http.StatusUnprocessableEntity, "preflight_invalid_source",
			"Not a GitHub repository", "Paste a public github.com repository URL, or owner/repo.")
	default:
		writeProblem(w, http.StatusBadGateway, "preflight_check_failed",
			"Check failed", "The repository could not be inspected.")
	}
}

// clientIP prefers the left-most X-Forwarded-For entry because apid sits
// behind the trusted Caddy/Cloudflare edge; RemoteAddr there is the proxy.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, found := strings.Cut(forwarded, ","); found || first != "" {
			return strings.TrimSpace(first)
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// reportCache is a bounded TTL cache. A verdict is a pure function of the
// commit it came from, so a hit is always as correct as a recomputation.
type reportCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	max     int
	ttl     time.Duration
}

type cacheEntry struct {
	report  Report
	expires time.Time
}

func newReportCache(max int, ttl time.Duration) *reportCache {
	return &reportCache{entries: make(map[string]cacheEntry), max: max, ttl: ttl}
}

func (c *reportCache) get(key string) (Report, bool) {
	if key == "" {
		return Report{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(c.entries, key)
		return Report{}, false
	}
	return entry.report, true
}

func (c *reportCache) put(key string, report Report) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		c.evictExpiredLocked()
	}
	if len(c.entries) >= c.max {
		return
	}
	c.entries[key] = cacheEntry{report: report, expires: time.Now().Add(c.ttl)}
}

func (c *reportCache) evictExpiredLocked() {
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
}

// ipLimiter is a fixed-window counter per client IP.
type ipLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	limit   int
	period  time.Duration
}

type window struct {
	count int
	reset time.Time
}

func newIPLimiter(limit int, period time.Duration) *ipLimiter {
	return &ipLimiter{windows: make(map[string]*window), limit: limit, period: period}
}

func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.windows[ip]
	if !ok || now.After(current.reset) {
		if len(l.windows) > 10_000 {
			l.pruneLocked(now)
		}
		l.windows[ip] = &window{count: 1, reset: now.Add(l.period)}
		return true
	}
	if current.count >= l.limit {
		return false
	}
	current.count++
	return true
}

func (l *ipLimiter) pruneLocked(now time.Time) {
	for ip, w := range l.windows {
		if now.After(w.reset) {
			delete(l.windows, ip)
		}
	}
}
