// Per-instance in-flight ForwardHTTP request counter. See
// doc.go for the design, regression contract, and streaming
// forward-compat note (issue #490).

package activity

import (
	"sync"
	"time"
)

// instanceState is the per-instance reading held by the
// ActivityTracker. Stamped on every Begin; never reset by End
// (only Forget does that).
type dispatchKey struct {
	id      string
	attempt int
}

type instanceState struct {
	dispatches map[dispatchKey]int
	inflight   int64
	executions int64
	lastAt     time.Time
	// total is the monotonic number of ForwardHTTP requests observed
	// for this instance since the tracker was created. Keeping this
	// beside inflight makes the scheduler's scale signal local to the
	// vmmd↔schedd stats path; it does not require Prometheus to reach
	// across compute nodes.
	total uint64
	// quietAt is when inflight last dropped to zero: the end of the
	// instance's last request. ADR-1151's quiet pause measures idleness
	// from here, not from the request's start.
	quietAt time.Time
}

// ActivityTracker tracks the in-flight ForwardHTTP request
// count per vmmd instance. Construct with New (testable) or
// NewWithDefaults (production cmd/vmmd wiring).
type ActivityTracker struct {
	mu    sync.Mutex
	state map[string]*instanceState
	now   func() time.Time
}

// New returns an ActivityTracker. now is consulted on every
// Begin to stamp lastAt; pass nil to use time.Now.
func New(now func() time.Time) *ActivityTracker {
	if now == nil {
		now = time.Now
	}
	return &ActivityTracker{state: make(map[string]*instanceState), now: now}
}

// NewWithDefaults returns an ActivityTracker that uses time.Now.
// Intended for cmd/vmmd wiring.
func NewWithDefaults() *ActivityTracker { return New(nil) }

// Begin increments the in-flight counter for instanceID and
// stamps lastAt to the wall-clock now. Empty instanceID is a
// no-op (forward.go's caller already validates, but the cache
// itself is defensive for any future caller).
func (a *ActivityTracker) Begin(instanceID string) { a.begin(instanceID, "", 0) }

func (a *ActivityTracker) begin(instanceID, invocation string, attempt int) {
	if instanceID == "" {
		return
	}
	a.mu.Lock()
	s, ok := a.state[instanceID]
	if !ok {
		s = &instanceState{}
		a.state[instanceID] = s
	}
	if invocation != "" && attempt > 0 {
		if s.dispatches == nil {
			s.dispatches = map[dispatchKey]int{}
		}
		s.dispatches[dispatchKey{invocation, attempt}]++
	}
	s.inflight++
	s.total++
	s.lastAt = a.now()
	a.mu.Unlock()
}

// End decrements the in-flight counter for instanceID. Empty
// instanceID is a no-op; missing state is a no-op (idempotent
// — see Regression contract in doc.go). inflight is clamped
// at zero so a stray late-arriving End (post-Destroy) cannot
// drive the gauge negative.
func (a *ActivityTracker) End(instanceID string) { a.end(instanceID, "", 0) }

func (a *ActivityTracker) end(instanceID, invocation string, attempt int) {
	if instanceID == "" {
		return
	}
	a.mu.Lock()
	s, ok := a.state[instanceID]
	if !ok {
		a.mu.Unlock()
		return
	}
	if invocation != "" && attempt > 0 {
		key := dispatchKey{invocation, attempt}
		if s.dispatches[key] <= 1 {
			delete(s.dispatches, key)
		} else {
			s.dispatches[key]--
		}
	}
	if s.inflight > 0 {
		s.inflight--
		if s.inflight == 0 && s.executions == 0 {
			s.quietAt = a.now()
		}
	}
	a.mu.Unlock()
}

// Quiet returns the in-flight count and, when it is zero, the time the
// instance's last request ended. ok is false for an instance never seen.
// A request that began but has not ended keeps inflight above zero, so a
// zero count always pairs with a set quiet time.
func (a *ActivityTracker) Quiet(instanceID string) (inflight int64, since time.Time, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.state[instanceID]
	if !ok {
		return 0, time.Time{}, false
	}
	return max(s.inflight, s.executions), s.quietAt, true
}

// Inflight returns the current in-flight count and a
// "seen-ever" boolean. (0, false) when the cache has never
// observed instanceID. Mirrors cpustats.Snapshot semantics.
func (a *ActivityTracker) Inflight(instanceID string) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.state[instanceID]
	if !ok {
		return 0, false
	}
	return max(s.inflight, s.executions), true
}

// BeginExecution pins execution independently of its HTTP transport. Every
// qualified function request owns a lease, so max(forwards, executions) avoids
// double-counting foreground overlap while retaining post-response activity.
// It never increments the HTTP request counter used by scaling.
func (a *ActivityTracker) BeginExecution(instance string) {
	if instance == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state[instance]
	if s == nil {
		s = &instanceState{}
		a.state[instance] = s
	}
	s.executions++
}

func (a *ActivityTracker) EndExecution(instance string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.state[instance]; s != nil && s.executions > 0 {
		s.executions--
		if s.executions == 0 && s.inflight == 0 {
			s.quietAt = a.now()
		}
	}
}

// LastAt returns the most-recent Begin moment and a
// "seen-ever" boolean. (zero time, false) when the cache has
// never observed instanceID.
func (a *ActivityTracker) LastAt(instanceID string) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.state[instanceID]
	if !ok {
		return time.Time{}, false
	}
	return s.lastAt, true
}

// Total returns the cumulative number of ForwardHTTP requests observed for
// instanceID and a "seen-ever" boolean. The counter resets when vmmd restarts
// or when Forget is called for a destroyed instance; consumers must treat a
// decrease as a new baseline.
func (a *ActivityTracker) Total(instanceID string) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.state[instanceID]
	if !ok {
		return 0, false
	}
	return s.total, true
}

// Forget drops per-instance state. Called from
// vmmdgrpc.Server.Destroy so the map does not grow unbounded
// across the vmmd process lifetime.
func (a *ActivityTracker) Forget(instanceID string) {
	a.mu.Lock()
	delete(a.state, instanceID)
	a.mu.Unlock()
}

// Reset wipes all per-instance state. Used by tests and as the
// documented seam for any future "drop everything" admin
// operation.
func (a *ActivityTracker) Reset() {
	a.mu.Lock()
	a.state = make(map[string]*instanceState)
	a.mu.Unlock()
}

// Size returns the number of instances currently tracked. For
// diagnostics and tests; not used on the hot path.
func (a *ActivityTracker) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.state)
}

// BeginDispatch and EndDispatch retain gateway-authored managed invocation
// identity for a cooperative rehearsal. They account for the entire forward.
func (a *ActivityTracker) BeginDispatch(instance, invocation string, attempt int) {
	a.begin(instance, invocation, attempt)
}
func (a *ActivityTracker) EndDispatch(instance, invocation string, attempt int) {
	a.end(instance, invocation, attempt)
}

// OnlyDispatch refuses missing, duplicate or concurrent forwards and execution
// ownership whose dispatch cannot be proven. Ordinary
// Freeze continues to use the full inflight count, without this exception.
func (a *ActivityTracker) OnlyDispatch(instance, invocation string, attempt int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state[instance]
	return invocation != "" && attempt > 0 && s != nil && s.executions == 0 && s.inflight == 1 && len(s.dispatches) == 1 && s.dispatches[dispatchKey{invocation, attempt}] == 1
}
