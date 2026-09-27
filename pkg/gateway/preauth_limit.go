package gateway

import (
	"container/list"
	"context"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// preAuthSourcesPerApp caps traffic-controlled bucket cardinality per policy.
// A source beyond this cap shares that policy's overflow bucket until a fully refilled
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

type preAuthFailureContextKey struct{}
type preAuthShadowContextKey struct{}

// At most the app, one exact route, and that route's failure budget can
// shadow-block a request. Only fixed, bounded policy slots enter metrics.
type preAuthShadowContext struct {
	appID    string
	policies [3]string
	n        int
}

func (s *preAuthShadowContext) add(policy string) {
	if s.n < len(s.policies) {
		s.policies[s.n] = policy
		s.n++
	}
}

type preAuthFailureContext struct {
	appID    string
	policyID string
	source   string
	rps      float64
	burst    int
	statuses [4]int
	statusN  int
}

func (f preAuthFailureContext) tracks(status int) bool {
	if f.statusN == 0 {
		return status == http.StatusUnauthorized || status == http.StatusForbidden
	}
	for _, selected := range f.statuses[:f.statusN] {
		if selected == status {
			return true
		}
	}
	return false
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
	allowed, _ := l.allowRate(appID, source, float64(rps), burst, true, false)
	return allowed
}

// AvailableRate checks a response-driven budget without spending a token.
// The later application response spends one token only for a selected status.
func (l *preAuthSourceLimiter) AvailableRate(appID, source string, rps float64, burst int) (bool, int) {
	return l.allowRate(appID, source, rps, burst, false, false)
}

// RecordRate spends a response token even when concurrent in-flight failures
// have already exhausted the budget. The resulting debt delays the next admit.
func (l *preAuthSourceLimiter) RecordRate(appID, source string, rps float64, burst int) {
	l.allowRate(appID, source, rps, burst, true, true)
}

func (l *preAuthSourceLimiter) allowRate(appID, source string, rps float64, burst int, consume, allowDebt bool) (bool, int) {
	if l == nil || appID == "" || source == "" || rps <= 0 || burst <= 0 {
		return false, 1
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
			b = &preAuthBucket{tokens: float64(burst), last: now, rps: rps, burst: float64(burst), source: source, appID: appID}
			b.elem = app.recent.PushFront(b)
			b.global = l.recent.PushFront(b)
			app.sources[source] = b
			l.sourceCount++
		} else {
			if app.overflow == nil {
				app.overflow = &preAuthBucket{tokens: float64(burst), last: now, rps: rps, burst: float64(burst)}
			}
			b = app.overflow
		}
	} else {
		app.recent.MoveToFront(b.elem)
		l.recent.MoveToFront(b.global)
	}
	b.refill(now, rps, float64(burst))
	// Float refill can land a few ulps below one token at the advertised
	// Retry-After boundary (for example 5/60 tokens per second at 24s).
	if b.tokens < 1-1e-9 && !allowDebt {
		return false, max(1, int(math.Ceil((1-b.tokens)/rps)))
	}
	if consume {
		b.tokens--
	}
	return true, 0
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
	source := ip.String()
	shadow := preAuthShadowContext{appID: app.ID}
	var matched *api.PreAuthRouteLimit
	matchedIndex := -1
	if len(config.Routes) > 0 {
		// Match the decoded public path before edge rewrites. Cleaning covers
		// common router normalization, so /login/../login cannot bypass an
		// exact /login policy when the application normalizes that path.
		publicPath := path.Clean(strings.ReplaceAll(r.URL.Path, "\\", "/"))
		for i := range config.Routes {
			route := &config.Routes[i]
			if route.Method == r.Method && route.Path == publicPath {
				matched = route
				matchedIndex = i
				break
			}
		}
	}
	if matched != nil && matched.FailedResponses != nil {
		failed := matched.FailedResponses
		failure := preAuthFailureContext{
			appID: app.ID, policyID: app.ID + "\x00" + matched.Method + " " + matched.Path + "\x00failures",
			source: source, rps: float64(min(failed.FailuresPerMinute, min(matched.RequestsPerSecond, rps)*60)) / 60,
			burst: min(failed.Burst, min(matched.Burst, burst)), statusN: min(len(failed.Statuses), 4),
		}
		copy(failure.statuses[:], failed.Statuses)
		// Preserve the verified source and public route through edge header
		// mutation and path rewriting; only a proxied application response
		// can spend this budget after the request completes.
		*r = *r.WithContext(context.WithValue(r.Context(), preAuthFailureContextKey{}, failure))
		if available, retryAfter := h.preAuthLimiter.AvailableRate(failure.policyID, source, failure.rps, failure.burst); !available {
			if config.Mode == api.PreAuthRateLimitObserve {
				policy := "failures_" + strconv.Itoa(matchedIndex)
				shadow.add(policy)
				h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "would_block")
			}
			if h.metrics != nil {
				outcome := "failure_blocked"
				if config.Mode == api.PreAuthRateLimitObserve {
					outcome = "failure_would_block"
				}
				h.metrics.ObservePreAuthRateLimit(app.ID, outcome)
			}
			if config.Mode == api.PreAuthRateLimitEnforce {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.Header().Set("x-faas-rate-limit-scope", "pre-auth-failures")
				api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
					"Pre-auth rate limit exceeded", "this source has sent too many requests"))
				h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
				return true
			}
		}
	}
	allowed := h.preAuthLimiter.Allow(app.ID, source, rps, burst)
	scope := "pre-auth"
	routeAllowed := true
	if matched != nil && (allowed || config.Mode == api.PreAuthRateLimitObserve) {
		policyID := app.ID + "\x00" + matched.Method + " " + matched.Path
		routeAllowed = h.preAuthLimiter.Allow(policyID, source, min(matched.RequestsPerSecond, rps), min(matched.Burst, burst))
		if allowed {
			scope = "pre-auth-route"
		}
	}
	if config.Mode == api.PreAuthRateLimitObserve {
		if !allowed {
			shadow.add("app")
			h.metrics.ObservePreAuthPolicyShadow(app.ID, "app", "would_block")
		}
		if !routeAllowed {
			policy := "route_" + strconv.Itoa(matchedIndex)
			shadow.add(policy)
			h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "would_block")
		}
		if shadow.n > 0 {
			*r = *r.WithContext(context.WithValue(r.Context(), preAuthShadowContextKey{}, shadow))
		}
		if h.metrics != nil && (!allowed || !routeAllowed) {
			outcome := "would_block"
			if !routeAllowed && allowed {
				outcome = "route_would_block"
			}
			h.metrics.ObservePreAuthRateLimit(app.ID, outcome)
		}
		return false
	}
	if allowed && routeAllowed {
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

// recordPreAuthShadowResult completes each observe-mode would-block decision
// with the final gateway response class, including auth and wake errors.
func (h *Handler) recordPreAuthShadowResult(r *http.Request, status int) {
	if h == nil || h.metrics == nil || r == nil {
		return
	}
	shadow, ok := r.Context().Value(preAuthShadowContextKey{}).(preAuthShadowContext)
	if !ok {
		return
	}
	outcome := "result_unknown"
	if status >= 200 && status <= 599 {
		outcome = "result_" + strconv.Itoa(status/100) + "xx"
	}
	for _, policy := range shadow.policies[:shadow.n] {
		h.metrics.ObservePreAuthPolicyShadow(shadow.appID, policy, outcome)
	}
}

// recordPreAuthFailedResponse is called only after an application proxy leg.
// Gateway auth denials, cache responses, and wake errors do not spend tokens.
func (h *Handler) recordPreAuthFailedResponse(r *http.Request, status int) {
	if h == nil || h.preAuthLimiter == nil || r == nil {
		return
	}
	failure, ok := r.Context().Value(preAuthFailureContextKey{}).(preAuthFailureContext)
	if !ok || !failure.tracks(status) {
		return
	}
	h.preAuthLimiter.RecordRate(failure.policyID, failure.source, failure.rps, failure.burst)
	if h.metrics != nil {
		h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_recorded")
	}
}

// ForgetPreAuthRateLimits clears process-local source buckets on SIGHUP.
func (h *Handler) ForgetPreAuthRateLimits() int {
	if h == nil {
		return 0
	}
	return h.preAuthLimiter.ForgetAll()
}
