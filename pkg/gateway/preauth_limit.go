package gateway

import (
	"container/list"
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/netip"
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

// A fixed number of shared counters per exact route bounds database growth
// even when an attacker rotates source addresses. Collisions share a budget.
const preAuthCentralShards = 1024
const preAuthTargetShards = 2048

var errPreAuthCentralUnavailable = errors.New("pre-auth central rate-limit backend unavailable")

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
	appID          string
	policyID       string
	policyIndex    int
	observeTargets bool
	centralSubject string
	plan           string
	source         string
	rps            float64
	burst          int
	statuses       [4]int
	statusN        int
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

// mirrorCentralBalance keeps the process-local degraded fallback conservative.
// A concurrent older response must never restore tokens spent by a newer one.
func (l *preAuthSourceLimiter) mirrorCentralBalance(policyID, source string, remaining int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	app := l.apps[policyID]
	if app == nil {
		return
	}
	b := app.sources[source]
	if b == nil {
		b = app.overflow
	}
	if b != nil {
		b.tokens = min(b.tokens, float64(remaining))
		b.last = l.now()
	}
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
	markTrafficPhase(r.Context(), trafficRates)
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
			policyIndex: matchedIndex, observeTargets: matched.ObserveTargets,
			plan:   string(app.Plan),
			source: source, rps: float64(min(failed.FailuresPerMinute, min(matched.RequestsPerSecond, rps)*60)) / 60,
			burst: min(failed.Burst, min(matched.Burst, burst)), statusN: min(len(failed.Statuses), 4),
		}
		if failed.Coordination == api.PreAuthCoordinationCentral {
			failure.centralSubject = dimensionalCentralSubjectID(failure.policyID, "source_ip", source, preAuthCentralShards)
		}
		copy(failure.statuses[:], failed.Statuses)
		// Preserve the verified source and public route through edge header
		// mutation and path rewriting; only a proxied application response
		// can spend this budget after the request completes.
		*r = *r.WithContext(context.WithValue(r.Context(), preAuthFailureContextKey{}, failure))
		available, retryAfter := h.preAuthLimiter.AvailableRate(failure.policyID, source, failure.rps, failure.burst)
		if failure.centralSubject != "" {
			available, retryAfter = h.checkCentralPreAuthFailure(r.Context(), failure, available, retryAfter)
		}
		if !available {
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
				recordTrafficLimiter(r.Context(), "surface")
				recordTrafficRefusal(r.Context(), "rate_limited")
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
		routeRPS := min(matched.RequestsPerSecond, rps)
		routeBurst := min(matched.Burst, burst)
		routeAllowed = h.preAuthLimiter.Allow(policyID, source, routeRPS, routeBurst)
		if matched.Coordination == api.PreAuthCoordinationCentral {
			routeAllowed = h.allowCentralPreAuthRoute(r.Context(), app, policyID, source, routeRPS, routeBurst, routeAllowed)
		}
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
	recordTrafficLimiter(r.Context(), "surface")
	recordTrafficRefusal(r.Context(), "rate_limited")
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

func (h *Handler) checkCentralPreAuthFailure(ctx context.Context, failure preAuthFailureContext, localAllowed bool, localRetry int) (bool, int) {
	backend, ok := h.preAuthCentral.(CentralFailureBackend)
	if !ok || backend == nil {
		h.observeCentralRateLimitDegraded(ctx, rateLimitScopePreAuth, errPreAuthCentralUnavailable)
		if h.metrics != nil {
			h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_central_fallback")
		}
		return localAllowed, localRetry
	}
	consultCtx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
	defer cancel()
	centralAllowed, centralRetry, err := backend.CheckPreAuthFailure(consultCtx, failure.centralSubject, failure.plan, failure.rps, failure.burst)
	if err != nil {
		h.observeCentralRateLimitDegraded(ctx, rateLimitScopePreAuth, err)
		if h.metrics != nil {
			h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_central_fallback")
		}
		return localAllowed, localRetry
	}
	// A local failure can have been recorded while the central store was
	// unavailable. Keep that debt after recovery rather than forgiving it.
	return centralAllowed && localAllowed, max(centralRetry, localRetry)
}

// allowCentralPreAuthRoute consults one bounded shared counter after spending
// the local fallback token. A central decision is authoritative; database
// errors fall back to the already spent local bucket and are observable.
func (h *Handler) allowCentralPreAuthRoute(ctx context.Context, app App, policyID, source string, rps, burst int, localAllowed bool) bool {
	_, noop := h.preAuthCentral.(noopCentralBackend)
	if h.preAuthCentral == nil || noop {
		h.observeCentralRateLimitDegraded(ctx, rateLimitScopePreAuth, errPreAuthCentralUnavailable)
		h.metrics.ObservePreAuthRateLimit(app.ID, "central_fallback")
		return localAllowed
	}
	subjectID := dimensionalCentralSubjectID(policyID, "source_ip", source, preAuthCentralShards)
	consultCtx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
	defer cancel()
	remaining, admitted, err := h.preAuthCentral.ConsumeToken(consultCtx, rateLimitScopePreAuth, subjectID, string(app.Plan), float64(rps), float64(burst))
	if err != nil {
		h.observeCentralRateLimitDegraded(ctx, rateLimitScopePreAuth, err)
		h.metrics.ObservePreAuthRateLimit(app.ID, "central_fallback")
		return localAllowed
	}
	h.preAuthLimiter.mirrorCentralBalance(policyID, source, remaining)
	return admitted
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
	if failure.centralSubject != "" {
		backend, ok := h.preAuthCentral.(CentralFailureBackend)
		if !ok || backend == nil {
			h.observeCentralRateLimitDegraded(r.Context(), rateLimitScopePreAuth, errPreAuthCentralUnavailable)
			if h.metrics != nil {
				h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_central_fallback")
			}
		} else {
			// A disconnect must not cancel recording a completed app failure.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), centralConsultTimeout)
			err := backend.RecordPreAuthFailure(ctx, failure.centralSubject, failure.plan, failure.rps, failure.burst)
			cancel()
			if err != nil {
				h.observeCentralRateLimitDegraded(r.Context(), rateLimitScopePreAuth, err)
				if h.metrics != nil {
					h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_central_fallback")
				}
			}
		}
	}
	if h.metrics != nil {
		h.metrics.ObservePreAuthRateLimit(failure.appID, "failure_recorded")
	}
}

// recordPreAuthTargetResponse correlates selected application failures across
// source IPs. Two independently selected, bounded shards reduce accidental
// collisions. The header value is never persisted, logged, or exposed as a
// metric label; the signal cannot reject requests or lock an account.
func (h *Handler) recordPreAuthTargetResponse(r *http.Request, rec *statusRecorder, app App) {
	if h == nil || h.preAuthLimiter == nil || h.metrics == nil || r == nil || rec == nil {
		return
	}
	failure, ok := r.Context().Value(preAuthFailureContextKey{}).(preAuthFailureContext)
	if !ok || !failure.observeTargets || !failure.tracks(rec.status) {
		return
	}
	policy := "targets_" + strconv.Itoa(failure.policyIndex)
	if rec.preAuthTargetCount == 0 {
		h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_missing")
		return
	}
	target := rec.preAuthTarget
	if rec.preAuthTargetCount != 1 || !validPreAuthTarget(target) {
		h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_invalid")
		return
	}
	h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_failure")
	policyID := failure.policyID + "\x00targets"
	localExceeded := true
	var subjects [2]string
	for i := range subjects {
		dimension := "login_target_" + strconv.Itoa(i)
		subjects[i] = dimensionalCentralSubjectID(policyID, dimension, target, preAuthTargetShards)
		admitted, _ := h.preAuthLimiter.allowRate(policyID+"\x00"+dimension, subjects[i], failure.rps, failure.burst, true, false)
		if admitted {
			localExceeded = false
		}
	}
	_, noop := h.preAuthCentral.(noopCentralBackend)
	if h.preAuthCentral == nil || noop {
		h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_fallback")
		if localExceeded {
			h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_threshold")
		}
		return
	}
	// Recording follows an application failure even if the client has already
	// disconnected. Keep the database work bounded without letting a caller
	// cancel its own abuse observation.
	consultCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), centralConsultTimeout)
	defer cancel()
	centralExceeded := true
	for _, subject := range subjects {
		_, admitted, err := h.preAuthCentral.ConsumeToken(consultCtx, rateLimitScopePreAuth, subject, string(app.Plan), failure.rps, float64(failure.burst))
		if err != nil {
			h.observeCentralRateLimitDegraded(r.Context(), rateLimitScopePreAuth, err)
			h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_fallback")
			if localExceeded {
				h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_threshold")
			}
			return
		}
		if admitted {
			centralExceeded = false
		}
	}
	if centralExceeded {
		h.metrics.ObservePreAuthPolicyShadow(app.ID, policy, "target_threshold")
	}
}

func validPreAuthTarget(target string) bool {
	// The application sends a keyed digest of its normalized login value.
	// Enforcing the digest format prevents accidental raw identifier leakage
	// through the gateway's internal response path.
	if len(target) != 64 {
		return false
	}
	for i := 0; i < len(target); i++ {
		char := target[i]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
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
