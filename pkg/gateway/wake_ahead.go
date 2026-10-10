package gateway

// Opt-in service wake-ahead along measured edges (ADR-956, amending ADR-196).
//
// ADR-196 holds a cold internal call while its parked target restores, so a
// fully cold chain public-api → auth → billing pays its restores one after
// another. Wake-ahead starts a target's restore when its caller starts waking,
// but only for edges this gateway has measured: after a cold wake of a caller,
// a service call from it inside the follow window is a hit for that edge, and
// a hit only counts as a benefit when the target was parked (or warm only
// because a wake-ahead already restored it).
//
// The learner is node-local and in memory. A wake-ahead is an ordinary wake
// through the same WakeGate and scheduler admission as a request; it only
// differs by trigger, and it starts only below the fleet residency guard so a
// prediction never competes for the last of the RAM ceiling.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wake-ahead outcomes recorded in gateway_service_wake_ahead_total.
const (
	WakeAheadStarted          = "started"
	WakeAheadUsed             = "used"
	WakeAheadUnused           = "unused"
	WakeAheadSkippedResidency = "skipped_residency"
	WakeAheadFailed           = "failed"
)

// WakeAheadLearner measures caller → target service edges around caller wakes.
type WakeAheadLearner struct {
	mu          sync.Mutex
	now         func() time.Time
	outcome     func(string)
	callers     map[string]*wakeAheadCaller
	speculative map[string]*wakeAheadSpeculation
}

type wakeAheadCaller struct {
	windowStart time.Time
	open        bool
	called      map[string]bool // target → the call found it parked or pre-woken
	wakes       float64
	edges       map[string]*wakeAheadEdge
}

type wakeAheadEdge struct{ calls, cold float64 }

type wakeAheadSpeculation struct {
	at   time.Time
	used bool
}

// NewWakeAheadLearner returns an empty learner. outcome receives used and
// unused verdicts for wake-aheads; it may be nil.
func NewWakeAheadLearner(now func() time.Time, outcome func(string)) *WakeAheadLearner {
	if now == nil {
		now = time.Now
	}
	if outcome == nil {
		outcome = func(string) {}
	}
	return &WakeAheadLearner{now: now, outcome: outcome, callers: map[string]*wakeAheadCaller{}, speculative: map[string]*wakeAheadSpeculation{}}
}

// ObserveWake records that app is starting a wake. It reports false when the
// wake falls inside the app's open follow window: a burst of requests into one
// cold app is one wake.
func (l *WakeAheadLearner) ObserveWake(app string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)
	c := l.callers[app]
	if c == nil {
		l.evictLocked()
		c = &wakeAheadCaller{called: map[string]bool{}, edges: map[string]*wakeAheadEdge{}}
		l.callers[app] = c
	}
	if c.open && now.Sub(c.windowStart) <= api.ServiceWakeAheadFollowWindow {
		return false
	}
	c.finishLocked()
	c.open, c.windowStart = true, now
	return true
}

// ObserveCall records one service call that reached the target's guest.
// woken is true when the call itself had to restore the target.
func (l *WakeAheadLearner) ObserveCall(caller, target string, woken bool) {
	if caller == "" || target == "" || caller == target {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	benefit := woken
	if s := l.speculative[target]; s != nil && !woken && now.Sub(s.at) <= api.ServiceWakeAheadFollowWindow {
		benefit = true
		if !s.used {
			s.used = true
			l.outcome(WakeAheadUsed)
		}
	}
	c := l.callers[caller]
	if c == nil || !c.open || now.Sub(c.windowStart) > api.ServiceWakeAheadFollowWindow {
		return
	}
	if _, seen := c.called[target]; !seen && len(c.called) >= api.ServiceWakeAheadMaxEdgesPerApp {
		return
	}
	c.called[target] = c.called[target] || benefit
}

// Predict returns the targets to wake ahead of app, best first.
func (l *WakeAheadLearner) Predict(app string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.callers[app]
	if c == nil {
		return nil
	}
	if c.open && l.now().Sub(c.windowStart) > api.ServiceWakeAheadFollowWindow {
		c.finishLocked()
	}
	if c.wakes < api.ServiceWakeAheadMinWakes {
		return nil
	}
	var targets []string
	for target, e := range c.edges {
		if e.calls/c.wakes >= api.ServiceWakeAheadMinCallShare && e.calls > 0 && e.cold/e.calls >= api.ServiceWakeAheadMinColdShare {
			targets = append(targets, target)
		}
	}
	slices.SortFunc(targets, func(a, b string) int {
		if c.edges[a].calls != c.edges[b].calls {
			if c.edges[a].calls > c.edges[b].calls {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	if len(targets) > api.ServiceWakeAheadMaxTargets {
		targets = targets[:api.ServiceWakeAheadMaxTargets]
	}
	return targets
}

// ClaimSpeculation reserves one wake-ahead of target. It fails while an
// earlier wake-ahead of the same target is still fresh.
func (l *WakeAheadLearner) ClaimSpeculation(target string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)
	if _, fresh := l.speculative[target]; fresh {
		return false
	}
	l.speculative[target] = &wakeAheadSpeculation{at: now}
	return true
}

// ReleaseSpeculation drops a claim whose wake did not happen.
func (l *WakeAheadLearner) ReleaseSpeculation(target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.speculative, target)
}

// finishLocked folds an expired follow window into the edge statistics.
func (c *wakeAheadCaller) finishLocked() {
	if !c.open {
		return
	}
	c.open = false
	c.wakes++
	for target, benefit := range c.called {
		e := c.edges[target]
		if e == nil {
			if len(c.edges) >= api.ServiceWakeAheadMaxEdgesPerApp {
				c.dropWeakestEdge()
			}
			e = &wakeAheadEdge{}
			c.edges[target] = e
		}
		e.calls++
		if benefit {
			e.cold++
		}
	}
	clear(c.called)
	if c.wakes >= api.ServiceWakeAheadDecayWakes {
		c.wakes /= 2
		for _, e := range c.edges {
			e.calls /= 2
			e.cold /= 2
		}
	}
}

func (c *wakeAheadCaller) dropWeakestEdge() {
	weakest := ""
	for target, e := range c.edges {
		if weakest == "" || e.calls < c.edges[weakest].calls || e.calls == c.edges[weakest].calls && target > weakest {
			weakest = target
		}
	}
	delete(c.edges, weakest)
}

// sweepLocked retires stale wake-ahead claims, counting unused predictions.
func (l *WakeAheadLearner) sweepLocked(now time.Time) {
	for target, s := range l.speculative {
		if now.Sub(s.at) > api.ServiceWakeAheadFollowWindow {
			if !s.used {
				l.outcome(WakeAheadUnused)
			}
			delete(l.speculative, target)
		}
	}
}

// evictLocked keeps the caller table bounded by dropping the caller whose
// last wake is oldest.
func (l *WakeAheadLearner) evictLocked() {
	if len(l.callers) < api.ServiceWakeAheadMaxApps {
		return
	}
	oldest := ""
	for app, c := range l.callers {
		if oldest == "" || c.windowStart.Before(l.callers[oldest].windowStart) {
			oldest = app
		}
	}
	delete(l.callers, oldest)
}

// WakeAheadConfig wires wake-ahead into a Handler. Every function must be set.
type WakeAheadConfig struct {
	Learner *WakeAheadLearner
	// Enabled reports whether the caller app opted in.
	Enabled func(ctx context.Context, appID string) (bool, error)
	// Residency reports fleet resident and ceiling RAM in MB.
	Residency func(ctx context.Context) (residentMB, ceilingMB int64, err error)
	// Wake restores one target; production ends in EnsureWakeAheadCapacity.
	Wake func(ctx context.Context, appID string) error
}

type wakeAheadRunner struct {
	cfg       WakeAheadConfig
	now       func() time.Time
	mu        sync.Mutex
	enabled   map[string]wakeAheadCached
	residency wakeAheadCached
}

type wakeAheadCached struct {
	ok      bool
	expires time.Time
}

type wakeAheadDepthKey struct{}

// SetWakeAhead enables ADR-956 wake-ahead. Without it the handler only wakes
// what is called, exactly as ADR-196 specifies.
func (h *Handler) SetWakeAhead(cfg WakeAheadConfig) {
	if cfg.Learner == nil || cfg.Enabled == nil || cfg.Residency == nil || cfg.Wake == nil {
		h.wakeAhead = nil
		return
	}
	h.wakeAhead = &wakeAheadRunner{cfg: cfg, now: time.Now, enabled: map[string]wakeAheadCached{}}
}

// ObserveServiceCall feeds the learner with a forwarded service call.
func (h *Handler) ObserveServiceCall(callerAppID, targetAppID string, woken bool) {
	if h.wakeAhead != nil {
		h.wakeAhead.cfg.Learner.ObserveCall(callerAppID, targetAppID, woken)
	}
}

// EnsureWakeAheadCapacity restores a predicted target through the ordinary
// wake machinery with the wake-ahead trigger. A target that is already
// running is left alone.
func (h *Handler) EnsureWakeAheadCapacity(ctx context.Context, app App) error {
	return h.ensureServiceCapacity(ctx, app, triggerServiceWakeAhead)
}

// noteColdWake records the start of appID's wake and, below the depth limit,
// starts wake-ahead of its measured targets in the background. It never
// delays the wake it observes.
func (h *Handler) noteColdWake(ctx context.Context, appID string) {
	r := h.wakeAhead
	if r == nil || appID == "" || h.backend.HealthyCount(appID) > 0 {
		return
	}
	if !r.cfg.Learner.ObserveWake(appID) {
		return
	}
	depth, _ := ctx.Value(wakeAheadDepthKey{}).(int)
	if depth >= api.ServiceWakeAheadMaxDepth {
		return
	}
	go h.runWakeAhead(appID, depth+1)
}

func (h *Handler) runWakeAhead(appID string, depth int) {
	r := h.wakeAhead
	ctx, cancel := context.WithTimeout(context.Background(), api.ServiceWakeAheadWakeTimeout)
	defer cancel()
	if !r.cached(ctx, appID) {
		return
	}
	targets := r.cfg.Learner.Predict(appID)
	if len(targets) == 0 {
		return
	}
	if !r.cached(ctx, "") {
		for range targets {
			h.metrics.IncServiceWakeAhead(WakeAheadSkippedResidency)
		}
		return
	}
	ctx = context.WithValue(ctx, wakeAheadDepthKey{}, depth)
	var wg sync.WaitGroup
	for _, target := range targets {
		if h.backend.HealthyCount(target) > 0 || !r.cfg.Learner.ClaimSpeculation(target) {
			continue
		}
		h.metrics.IncServiceWakeAhead(WakeAheadStarted)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.cfg.Wake(ctx, target); err != nil {
				r.cfg.Learner.ReleaseSpeculation(target)
				h.metrics.IncServiceWakeAhead(WakeAheadFailed)
				if h.log != nil {
					h.log.Warn("gateway: service wake-ahead failed", "caller_app", appID, "target_app", target, "err", err)
				}
			}
		}()
	}
	wg.Wait()
}

// cached answers the opt-in question for appID, or the residency guard for
// an empty appID, from a short-lived cache. A failed read is a no.
func (r *wakeAheadRunner) cached(ctx context.Context, appID string) bool {
	now := r.now()
	r.mu.Lock()
	entry, ok := r.enabled[appID]
	if appID == "" {
		entry, ok = r.residency, !r.residency.expires.IsZero()
	}
	r.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.ok
	}
	var allowed bool
	if appID == "" {
		resident, ceiling, err := r.cfg.Residency(ctx)
		if ceiling <= 0 {
			ceiling = api.RAMAdmissionCeilingMB
		}
		allowed = err == nil && resident*100 < ceiling*api.ServiceWakeAheadMaxResidentPercent
	} else {
		enabled, err := r.cfg.Enabled(ctx, appID)
		allowed = err == nil && enabled
	}
	entry = wakeAheadCached{ok: allowed, expires: now.Add(api.ServiceWakeAheadCacheTTL)}
	r.mu.Lock()
	if appID == "" {
		r.residency = entry
	} else {
		if len(r.enabled) >= api.ServiceWakeAheadMaxApps {
			clear(r.enabled)
		}
		r.enabled[appID] = entry
	}
	r.mu.Unlock()
	return allowed
}
