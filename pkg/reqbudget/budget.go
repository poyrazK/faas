// Package reqbudget: budget.go — Budget value, ctx carrier, per-hop
// wrap helpers. See doc.go for the package-level contract.
package reqbudget

import (
	"context"
	"sync"
	"time"
)

// Source tags how a Budget came into being. Useful for diagnostics
// (logs) and for picking ceiling defaults in production.
type Source string

const (
	SourceStdlib   Source = "stdlib"   // set by stdlib http.Server config (defensive)
	SourceEdge     Source = "edge"     // set by BudgetMiddleware at the edge
	SourceExplicit Source = "explicit" // set by an explicit per-route rule (kind=budget)
)

// HopMargin is an audit entry recording that a downstream hop
// reserved Cost wall-clock against the parent Remaining. Stacked on
// the Budget, surfaced in logs, and labelled on the
// request_budget_seconds histogram when that hop is the first to fire.
type HopMargin struct {
	Name string        // "db", "grpc", "http", "queue", "stream", "edge", …
	Cost time.Duration // reservation, not measurement
}

// Budget is the immutable value carried in context.Context. Total +
// Started + Ceiling are the three knobs that pin a deadline; the
// derived helpers (Remaining, WithOverhead, WithCeiling) read them.
// Overheads is an append-only audit trail of hop reservations. Now
// is the per-Budget clock — parents carry their clock handle to
// children so a test can fake the clock and still see elapsed time
// advance across the call sequence.
//
// A Budget has value semantics: it is safe to copy by value but the
// audit trail (Overheads) is only meaningful on the instance the
// per-hop helpers returned, not on the parent. Callers should always
// use the fresh Budget returned by WithOverhead / WithCeiling on
// downstream calls.
type Budget struct {
	Total     time.Duration    // originally allotted wall-clock
	Started   time.Time        // wall-clock anchor
	Ceiling   time.Duration    // hard upper bound on Remaining at this hop
	Overheads []HopMargin      // reserved costs to date (audit trail)
	Endpoint  string           // "POST:/payment" — metric + log label
	Route     string           // "forward" | "admin" | "invoke" | "edge"
	Source    Source           // diagnostic tag
	Now       func() time.Time // per-Budget clock; nil → time.Now
}

// DefaultClock is the production wall clock. Tests override
// per-Budget via Budget.Now so the package doesn't need a global.
var DefaultClock = time.Now

// now returns b.Now if set, else the package default. Production code
// leaves b.Now nil so this resolves to time.Now at every call.
func (b Budget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return DefaultClock()
}

// budgetKey is the unexported context key under which a Budget is
// stored. Unexported so callers can't introspect or collide.
type budgetKey struct{}

// budgetParentKey points at the context that existed before the request
// budget was attached. It lets a long-lived stream keep client/server
// cancellation while dropping only the platform budget after the response
// has started. The value is private so callers cannot accidentally replace
// the cancellation root.
type budgetParentKey struct{}

// FromContext returns the Budget attached to ctx and true, or the
// zero Budget and false when no Budget is attached. The bool is the
// load-bearing piece: call-sites without a budget (internal goroutines,
// admin paths that pre-date the middleware) take the no-op branch in
// WithOverhead / WithCeiling / WithRemaining.
func FromContext(ctx context.Context) (Budget, bool) {
	b, ok := ctx.Value(budgetKey{}).(Budget)
	return b, ok
}

// NewContext attaches b to parent. It does NOT install a deadline on
// the returned context — that is the responsibility of the caller
// (WithRemaining / WithOverhead / WithCeiling). NewContext exists so
// tests can pin a Budget onto a context without touching deadlines.
func NewContext(parent context.Context, b Budget) context.Context {
	return context.WithValue(parent, budgetKey{}, b)
}

func budgetBaseContext(parent context.Context) context.Context {
	if stored, ok := parent.Value(budgetParentKey{}).(context.Context); ok && stored != nil {
		return stored
	}
	return parent
}

// WithStream creates a context for a potentially long-lived response. Before
// detach is called, the context observes the request budget and cancels when
// it expires. After detach, the request budget is ignored, but cancellation
// and deadlines from the context that preceded the budget remain active.
//
// The returned cancel function must be called when the stream ends. detach is
// idempotent and should be called after response headers (the first response
// byte) have been committed. When no Budget is attached, WithStream is an
// identity wrapper with no budget to detach.
func WithStream(parent context.Context) (ctx context.Context, detach func(), cancel context.CancelFunc) {
	if _, ok := FromContext(parent); !ok {
		return parent, func() {}, func() {}
	}

	base := budgetBaseContext(parent)

	b, _ := FromContext(parent)
	remaining := b.Remaining(time.Time{})
	if remaining <= 0 {
		// Keep the original context so an already-expired request retains
		// its normal deadline/error semantics.
		return parent, func() {}, func() {}
	}

	s := &streamContext{
		current:  parent,
		base:     base,
		done:     make(chan struct{}),
		detached: make(chan struct{}),
		timer:    time.NewTimer(remaining),
	}
	go s.watch()

	return s, s.detach, s.cancel
}

// streamContext is deliberately small and lives here rather than in a
// gateway package so every request-serving daemon gets the same semantics.
// Value delegates to the current request context, except that a detached
// stream no longer exposes Budget to downstream hop helpers.
type streamContext struct {
	current  context.Context
	base     context.Context
	done     chan struct{}
	detached chan struct{}
	timer    *time.Timer

	mu         sync.RWMutex
	isDetached bool
	err        error
	closeOnce  sync.Once
	detachOnce sync.Once
}

func (s *streamContext) Deadline() (time.Time, bool) {
	s.mu.RLock()
	detached := s.isDetached
	s.mu.RUnlock()
	if detached {
		return s.base.Deadline()
	}
	b, ok := FromContext(s.current)
	if !ok || b.Total <= 0 {
		return s.base.Deadline()
	}
	budgetDeadline := b.Started.Add(b.Total)
	baseDeadline, hasBase := s.base.Deadline()
	if !hasBase || budgetDeadline.Before(baseDeadline) {
		return budgetDeadline, true
	}
	return baseDeadline, true
}

func (s *streamContext) Done() <-chan struct{} { return s.done }

func (s *streamContext) Err() error {
	s.mu.RLock()
	err := s.err
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return s.base.Err()
}

func (s *streamContext) Value(key any) any {
	if key == (budgetKey{}) {
		s.mu.RLock()
		detached := s.isDetached
		s.mu.RUnlock()
		if detached {
			return nil
		}
	}
	return s.current.Value(key)
}

func (s *streamContext) watch() {
	select {
	case <-s.base.Done():
		s.finish(s.base.Err())
	case <-s.timer.C:
		s.mu.RLock()
		detached := s.isDetached
		s.mu.RUnlock()
		if !detached {
			s.finish(context.DeadlineExceeded)
		}
	case <-s.detached:
		// The timer is no longer part of the stream after detach, but
		// the original cancellation root remains load-bearing.
		select {
		case <-s.base.Done():
			s.finish(s.base.Err())
		case <-s.done:
		}
	case <-s.done:
	}
}

func (s *streamContext) detach() {
	s.detachOnce.Do(func() {
		s.mu.Lock()
		s.isDetached = true
		s.mu.Unlock()
		if !s.timer.Stop() {
			select {
			case <-s.timer.C:
			default:
			}
		}
		close(s.detached)
	})
}

func (s *streamContext) cancel() {
	s.finish(context.Canceled)
}

func (s *streamContext) finish(err error) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		if !s.timer.Stop() {
			select {
			case <-s.timer.C:
			default:
			}
		}
		close(s.done)
	})
}

// Remaining is the wall-clock budget left at time `b.now()`. Negative
// remaining clamps to zero (a request that has already overshot its
// budget reports 0, not -3ms). Tests override b.Now for deterministic
// math; production leaves it nil and b.now() resolves to
// DefaultClock (time.Now).
func (b Budget) Remaining(_ time.Time) time.Duration {
	if b.Total <= 0 {
		return 0
	}
	now := b.now()
	elapsed := now.Sub(b.Started)
	if elapsed < 0 {
		elapsed = 0
	}
	r := b.Total - elapsed
	if r < 0 {
		return 0
	}
	return r
}

// derivedChild derives a child context whose deadline is
// min(parentRemaining, ceiling - sumOfOverheadsReserved). Both arguments
// are required; the helper never silently widens. The returned Budget
// has the new total (the capped value), preserves Started and the
// audit trail from parent.
//
// now is the wall-clock anchor passed in by the caller — read
// ONCE at the caller's entry so the math (remaining, childTotal,
// childStarted) is consistent within a single hop transition.
// Reading now twice (once inside b.Remaining and once via
// parentBudget.now() inside derivedChild) produces a
// time-of-check / time-of-use gap that lets the child overshoot
// parent's remaining by the read-1/read-2 delta — small with a
// wall clock, large with a fake clock advancing between calls.
//
// If parentBudget is the zero value (no Budget on ctx), derivedChild
// returns the parent ctx unchanged with a no-op cancel — an identity
// no-op so callers without a budget are unaffected.
func derivedChild(parentBudget Budget, parent context.Context, now time.Time, childTotal, ceiling time.Duration, hopName string, hopCost time.Duration) (context.Context, context.CancelFunc, Budget) {
	if parentBudget.Total == 0 {
		// No budget on ctx — be the parent, no cancellation churn.
		return parent, func() {}, Budget{}
	}
	if childTotal < 0 {
		childTotal = 0
	}
	childCtx, cancel := context.WithTimeout(parent, childTotal)
	childBudget := parentBudget
	childBudget.Total = childTotal
	childBudget.Ceiling = ceiling
	// Per-hop Started: the child's clock anchor is the moment of
	// attach. The caller passed `now` already read once at
	// entry, so childBudget.Remaining = childTotal - (now' -
	// childStarted) where now' >= now, and childTotal was
	// computed against this same `now` — the child's Remaining
	// math is internally consistent.
	childBudget.Started = now
	// Per-Budget clock handle: a child inherits its parent's clock so
	// tests can fake the wall clock end-to-end without touching
	// global state. Production leaves parentBudget.Now nil and the
	// clock handle resolves to DefaultClock at every call.
	childBudget.Now = parentBudget.Now
	// Defensive copy of Overheads: parentBudget.Overheads is a
	// slice header shared between parent and child after
	// `childBudget := parentBudget`. Without the copy, a
	// subsequent append() on the child can mutate the parent's
	// underlying array (if the parent's slice has spare
	// capacity), leaking the child's hop names back into the
	// parent's audit trail — every later hop on the parent
	// would then see the wrong history. The copy is cheap
	// (Overheads is bounded by hop count, typically ≤ 5) and
	// preserves the invariant that parent and child Overheads
	// are disjoint after a WithOverhead / WithCeiling call.
	if len(parentBudget.Overheads) > 0 {
		childBudget.Overheads = append(make([]HopMargin, 0, len(parentBudget.Overheads)+1), parentBudget.Overheads...)
	} else {
		childBudget.Overheads = nil
	}
	if hopName != "" && hopCost > 0 {
		childBudget.Overheads = append(childBudget.Overheads, HopMargin{Name: hopName, Cost: hopCost})
	}
	childCtx = context.WithValue(childCtx, budgetParentKey{}, budgetBaseContext(parent))
	return childCtx, cancel, childBudget
}

// WithCeiling is the wrap for hops with an absolute upper bound (JWT
// verify 5s, fwdStream 910s, rawStream 24h, dashboard PromQL 3s):
// child deadline = min(parentRemaining, ceiling). The ceiling is an
// absolute "this hop never takes more than X" — it can only tighten
// the parent budget, never loosen it. Returns the wrapped ctx, a
// cancel that MUST be defer'd by the caller, and the new child
// Budget.
//
// ceiling <= 0 is treated as "no ceiling configured" — the helper
// falls through to a child that inherits the parent's remaining
// time unchanged, with no fresh deadline installed. This matters
// for callers that pass a configured ceiling (e.g.
// http.Client.Timeout) which may be zero for a default-constructed
// client; treating zero as "tighten to zero" would produce a
// context.WithTimeout(parent, 0) ctx — already expired before the
// inner call runs. The caller can still install its own
// WithTimeout via the cli.Timeout path when the budget isn't
// attached.
//
// When no Budget is on parent, WithCeiling returns the parent ctx
// unchanged with a no-op cancel — call-sites without a budget don't
// change behavior.
func (b Budget) WithCeiling(parent context.Context, ceiling time.Duration) (context.Context, context.CancelFunc, Budget) {
	if b.Total == 0 {
		return parent, func() {}, Budget{}
	}
	// Read now ONCE at entry so the math is consistent within
	// this hop transition (Finding 4 from low-effort code
	// review: time-of-check / time-of-use gap otherwise lets
	// the child overshoot parent's remaining by the read-1/
	// read-2 delta).
	now := b.now()
	remaining := b.Total - now.Sub(b.Started)
	if remaining < 0 {
		remaining = 0
	}
	if ceiling <= 0 {
		// No ceiling configured. The child inherits the
		// parent's remaining time unchanged (childTotal =
		// childCeiling = remaining) — same shape as if the
		// caller hadn't wrapped at all, but the child Budget
		// is still stamped so a later WithOverhead records the
		// hop in the audit trail.
		return derivedChild(b, parent, now, remaining, remaining, "", 0)
	}
	childTotal := remaining
	if ceiling < childTotal {
		childTotal = ceiling
	}
	if childTotal < 0 {
		childTotal = 0
	}
	return derivedChild(b, parent, now, childTotal, ceiling, "", 0)
}

// WithOverhead is the per-hop workhorse: child deadline =
// min(parentRemaining - cost, parentCeiling - Σ(overheads)). cost is
// a reservation, not a measurement — it ensures hop B starts with
// less declared budget than hop A had even before B's own work
// begins.
//
// hopName goes onto the Budget.Overheads audit trail and is the
// `hop=...` label on the request_budget_seconds histogram when this
// hop is the first to fire.
//
// When no Budget is on parent, WithOverhead returns the parent ctx
// unchanged with a no-op cancel.
func (b Budget) WithOverhead(parent context.Context, hopName string, cost time.Duration) (context.Context, context.CancelFunc, Budget) {
	if b.Total == 0 {
		return parent, func() {}, Budget{}
	}
	// Read now ONCE at entry — see WithCeiling for the TOCTOU
	// rationale.
	now := b.now()
	remaining := b.Total - now.Sub(b.Started)
	if remaining < 0 {
		remaining = 0
	}
	if cost < 0 {
		cost = 0
	}
	childTotal := remaining - cost
	if childTotal < 0 {
		childTotal = 0
	}
	// Cap by the parent's ceiling so a child hop never exceeds the
	// ceiling even if parent remaining was generous. childCeiling is
	// carried on the new Budget for further descendants.
	return derivedChild(b, parent, now, childTotal, b.Ceiling, hopName, cost)
}

// WithRemaining is the edge setter: install a child ctx whose
// deadline is `total` (or any earlier parent deadline that may be on
// the incoming ctx — stdlib http.Server.WriteTimeout attaches a
// earlier deadline on r.Context()) with hard ceiling `ceiling`. The
// returned Budget is attached to the new ctx.
//
// route + endpoint are stamped onto the Budget for metric labels and
// log output. Pass "" for either to leave them blank.
//
// When no Budget is on parent and total > 0, WithRemaining installs
// one. When total <= 0, returns parent unchanged with a no-op cancel
// — useful for handlers that want to explicitly opt out (e.g., an
// admin long-poll).
func WithRemaining(parent context.Context, total, ceiling time.Duration, route, endpoint string) (context.Context, context.CancelFunc, Budget) {
	if total <= 0 {
		return parent, func() {}, Budget{}
	}
	if ceiling <= 0 || ceiling > total {
		ceiling = total
	}
	// Honor any earlier parent deadline: a stdlib http.Server that
	// attached a 60s ReadTimeout means we must not install a 3s
	// budget that ignores it — the tighter one wins.
	if dl, ok := parent.Deadline(); ok {
		untilParent := time.Until(dl)
		if untilParent > 0 && untilParent < total {
			total = untilParent
		}
	}
	started := DefaultClock()
	ctx, cancel := context.WithTimeout(parent, total)
	b := Budget{
		Total:    total,
		Started:  started,
		Ceiling:  ceiling,
		Route:    route,
		Endpoint: endpoint,
		Source:   SourceEdge,
	}
	ctx = context.WithValue(ctx, budgetParentKey{}, budgetBaseContext(parent))
	return context.WithValue(ctx, budgetKey{}, b), cancel, b
}
