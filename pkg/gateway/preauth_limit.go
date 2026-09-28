package gateway

import (
	"container/list"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// preAuthSourcesPerApp caps traffic-controlled bucket cardinality. A source
// beyond this cap shares the app's overflow bucket until a fully refilled
// source bucket can be safely evicted. In-flight rate debt is never reset by
// an attacker rotating addresses.
const preAuthSourcesPerApp = 1024
const preAuthTotalSources = 65_536
const preAuthEvictionScan = 32

type preAuthSourceLimiter struct {
	mu          sync.Mutex
	apps        map[string]*preAuthAppBuckets
	recent      *list.List
	sourceCount int
	maxSources  int
	now         func() time.Time
}

type preAuthAppBuckets struct {
	sources  map[string]*preAuthBucket
	recent   *list.List
	overflow *preAuthBucket
}

type preAuthBucket struct {
	tokens float64
	last   time.Time
	rps    float64
	burst  float64
	source string
	elem   *list.Element
	global *list.Element
	appID  string
}

func newPreAuthSourceLimiter() *preAuthSourceLimiter {
	return &preAuthSourceLimiter{apps: make(map[string]*preAuthAppBuckets), recent: list.New(), maxSources: preAuthTotalSources, now: time.Now}
}

func (l *preAuthSourceLimiter) evict(b *preAuthBucket) {
	app := l.apps[b.appID]
	delete(app.sources, b.source)
	app.recent.Remove(b.elem)
	l.recent.Remove(b.global)
	l.sourceCount--
}

// evictRefilled scans only a fixed number of oldest buckets. Fully refilled
// buckets have no outstanding rate debt, so evicting them cannot reset a
// depleted source's allowance.
func (l *preAuthSourceLimiter) evictRefilled(recent *list.List, now time.Time) bool {
	for el, scanned := recent.Back(), 0; el != nil && scanned < preAuthEvictionScan; scanned++ {
		previous := el.Prev()
		candidate := el.Value.(*preAuthBucket)
		candidate.refill(now, candidate.rps, candidate.burst)
		if candidate.tokens >= candidate.burst {
			l.evict(candidate)
			return true
		}
		el = previous
	}
	return false
}

// Allow applies one local source bucket. The existing app/account limiters
// remain the aggregate ceilings; this early bucket avoids authentication
// work for an individual source that exceeds its configured rate.
func (l *preAuthSourceLimiter) Allow(appID, source string, rps, burst int) bool {
	if l == nil || appID == "" || source == "" || rps <= 0 || burst <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	app := l.apps[appID]
	if app == nil {
		app = &preAuthAppBuckets{sources: make(map[string]*preAuthBucket), recent: list.New()}
		l.apps[appID] = app
	}
	b := app.sources[source]
	if b == nil {
		if len(app.sources) >= preAuthSourcesPerApp {
			l.evictRefilled(app.recent, now)
		}
		if l.sourceCount >= l.maxSources {
			l.evictRefilled(l.recent, now)
		}
		if len(app.sources) < preAuthSourcesPerApp && l.sourceCount < l.maxSources {
			b = &preAuthBucket{tokens: float64(burst), last: now, rps: float64(rps), burst: float64(burst), source: source, appID: appID}
			b.elem = app.recent.PushFront(b)
			b.global = l.recent.PushFront(b)
			app.sources[source] = b
			l.sourceCount++
		} else {
			if app.overflow == nil {
				app.overflow = &preAuthBucket{tokens: float64(burst), last: now, rps: float64(rps), burst: float64(burst)}
			}
			b = app.overflow
		}
	} else {
		app.recent.MoveToFront(b.elem)
		l.recent.MoveToFront(b.global)
	}
	b.refill(now, float64(rps), float64(burst))
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *preAuthBucket) refill(now time.Time, rps, burst float64) {
	b.tokens += now.Sub(b.last).Seconds() * rps
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last, b.rps, b.burst = now, rps, burst
}

func (l *preAuthSourceLimiter) ForgetAll() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.apps)
	l.apps = make(map[string]*preAuthAppBuckets)
	l.recent.Init()
	l.sourceCount = 0
	return n
}

// applyPreAuthRateLimit runs after trusted app routing and before consumer
// authentication. It deliberately uses only the public gateway's overwritten
// single-hop XFF value; arbitrary inbound headers never select a bucket.
func (h *Handler) applyPreAuthRateLimit(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App, deploymentSmoke bool) bool {
	config := app.PreAuthRateLimit
	if deploymentSmoke || config == nil || config.Mode == api.PreAuthRateLimitOff {
		return false
	}
	limits, planOK := api.LimitsFor(app.Plan)
	if !planOK || (config.Mode != api.PreAuthRateLimitObserve && config.Mode != api.PreAuthRateLimitEnforce) ||
		config.RequestsPerSecond <= 0 || config.Burst <= 0 || h.preAuthLimiter == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Pre-auth rate limit unavailable", "the app's pre-auth rate limit configuration is invalid"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return true
	}
	// A plan downgrade can make an older persisted policy exceed today's
	// ceiling. Clamp at read time, as the app-wide runtime limit does, so
	// the app remains reachable while still honoring its new plan.
	rps := min(config.RequestsPerSecond, limits.RateLimitRPS)
	burst := min(config.Burst, limits.RateLimitBurst)
	if err := config.ValidateRoutes(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Pre-auth rate limit unavailable", "the app's pre-auth rate limit configuration is invalid"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return true
	}
	ip, ok := clientIPFromTrustedXFF(r)
	if !ok {
		if config.Mode == api.PreAuthRateLimitObserve {
			if h.metrics != nil {
				h.metrics.ObservePreAuthRateLimit(app.ID, "untrusted_source")
			}
			return false
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Caller IP unavailable", "the gateway could not verify the client address"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return true
	}
	source := preAuthSourceKey(ip)
	allowed := h.preAuthLimiter.Allow(app.ID, source, rps, burst)
	scope := "pre-auth"
	if allowed && len(config.Routes) > 0 {
		// Match the decoded public path before edge rewrites. Cleaning covers
		// common router normalization, so /login/../login cannot bypass an
		// exact /login policy when the application normalizes that path.
		publicPath := path.Clean(strings.ReplaceAll(r.URL.Path, "\\", "/"))
		for _, route := range config.Routes {
			if route.Method != r.Method || route.Path != publicPath {
				continue
			}
			policyID := app.ID + "\x00" + route.Method + " " + route.Path
			allowed = h.preAuthLimiter.Allow(policyID, source, min(route.RequestsPerSecond, rps), min(route.Burst, burst))
			scope = "pre-auth-route"
			break
		}
	}
	if allowed {
		return false
	}
	if config.Mode == api.PreAuthRateLimitObserve {
		if h.metrics != nil {
			outcome := "would_block"
			if scope == "pre-auth-route" {
				outcome = "route_would_block"
			}
			h.metrics.ObservePreAuthRateLimit(app.ID, outcome)
		}
		return false
	}
	w.Header().Set("Retry-After", "1")
	w.Header().Set("x-faas-rate-limit-scope", scope)
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
		"Pre-auth rate limit exceeded", "this source has sent too many requests"))
	if h.metrics != nil {
		outcome := "blocked"
		if scope == "pre-auth-route" {
			outcome = "route_blocked"
		}
		h.metrics.ObservePreAuthRateLimit(app.ID, outcome)
	}
	h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
	return true
}

// ForgetPreAuthRateLimits clears process-local source buckets on SIGHUP.
func (h *Handler) ForgetPreAuthRateLimits() int {
	if h == nil {
		return 0
	}
	return h.preAuthLimiter.ForgetAll()
}

// preAuthSourceKey buckets IPv6 sources by /64. A subscriber is usually
// delegated a whole /64, so keying on the full address let one client
// rotate interface IDs for a fresh bucket per request (defeating the
// per-source login protection) and fill the shared source table, pushing
// every other app's new sources into its overflow bucket.
func preAuthSourceKey(ip net.IP) string {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return ip.String()
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}
