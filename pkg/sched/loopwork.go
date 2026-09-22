package sched

// loopwork.go — the bounded work pool behind Loop.Run's notification
// handler (ADR-191, decision 1).
//
// Loop.Run's select goroutine owns the pg_notify channel and eighteen
// tickers. Anything a handler arm does synchronously delays every other
// arm: the reaper, the §6.1 watchdog, cron, heartbeat. On 2026-09-03 a
// deadline-less PauseAndSnapshot held that goroutine for 10+ minutes and
// schedd did no work at all while looking healthy.
//
// dispatchPrime already moved one arm off the goroutine with a bounded
// four-slot pool. Four other arms escaped with a bare `go func`, which
// removes the head-of-line blocking but replaces it with unbounded
// goroutine growth: a burst of app_changed spawns one goroutine per
// notification, each taking the same per-app engine lock, all of them
// holding Postgres connections from a pool capped at 16.
//
// This pool is the single mechanism for both. Per-kind slot budgets bound
// the fan-out, a per-kind in-flight key coalesces duplicates, and the
// overflow policy is chosen per kind because the right answer differs:
//
//   - overflowInline (prime): run on the caller's goroutine when full.
//     A dropped snapshot_prime strands the deployment in `snapshotting`
//     with nothing to retry it — the notification is consumed and gone.
//     Blocking the loop is the lesser harm and is bounded by
//     SnapshotTimeout plus the cold-boot budget.
//   - overflowDrop (every reconcile kind): drop and count. Each one is an
//     idempotent reconcile over a durable table with a safety ticker
//     behind it, so a dropped duplicate costs at most one tick of
//     latency. Dropping is strictly better than unbounded goroutines.

import (
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// workKind is the closed set of off-loop task kinds. Each is a Prometheus
// label value, so the set stays small and fixed.
type workKind string

const (
	workPrime               workKind = "prime"
	workRestart             workKind = "restart"
	workAppReconcile        workKind = "app_reconcile"
	workDeploymentReconcile workKind = "deployment_reconcile"
	workJobCancel           workKind = "job_cancel"
)

// workKinds is the iteration order for metric pre-instantiation.
var workKinds = []workKind{
	workPrime, workRestart, workAppReconcile, workDeploymentReconcile, workJobCancel,
}

// overflowPolicy decides what submit does when a kind has no free slot.
type overflowPolicy int

const (
	// overflowDrop discards the task and counts it. Correct only for
	// idempotent work a safety ticker will retry.
	overflowDrop overflowPolicy = iota
	// overflowInline runs the task on the caller's goroutine. Correct
	// only where dropping would strand durable state.
	overflowInline
)

// workSpec is one kind's budget and overflow behaviour.
type workSpec struct {
	slots    int
	overflow overflowPolicy
}

// workSpecs is the per-kind policy table.
//
// prime keeps maxConcurrentPrimes (4): Prime takes the per-app engine
// lock, so concurrent primes for one app serialize anyway; the budget
// bounds cross-app fan-out.
//
// The reconcile kinds get 8 each. Most are short database-bound reconciles;
// a service deployment reconcile may also wait through ADR-208's bounded
// gateway-ack and request-drain barriers. It holds no database connection
// while waiting, and the eight-slot cap prevents a fleet-wide gateway issue
// from turning one stuck rollout into one unbounded goroutine.
var workSpecs = map[workKind]workSpec{
	workPrime:               {slots: maxConcurrentPrimes, overflow: overflowInline},
	workRestart:             {slots: 8, overflow: overflowDrop},
	workAppReconcile:        {slots: 8, overflow: overflowDrop},
	workDeploymentReconcile: {slots: 8, overflow: overflowDrop},
	workJobCancel:           {slots: 8, overflow: overflowDrop},
}

// workPool runs bounded, coalesced, off-loop tasks for Loop.
//
// Safe for concurrent use. The zero value is not usable; call newWorkPool.
type workPool struct {
	log *slog.Logger
	ops *wire.OpsMetrics

	slots map[workKind]chan struct{}

	mu       sync.Mutex
	inFlight map[workKind]map[string]struct{}

	// wg tracks dispatched workers so drain can wait for them. Tests
	// need this because submit moves work off the caller's goroutine.
	wg sync.WaitGroup
}

func newWorkPool(log *slog.Logger, ops *wire.OpsMetrics) *workPool {
	p := &workPool{
		log:      log,
		ops:      ops,
		slots:    make(map[workKind]chan struct{}, len(workSpecs)),
		inFlight: make(map[workKind]map[string]struct{}, len(workSpecs)),
	}
	for kind, spec := range workSpecs {
		p.slots[kind] = make(chan struct{}, spec.slots)
		p.inFlight[kind] = make(map[string]struct{})
	}
	return p
}

// submit runs fn off the caller's goroutine.
//
// key coalesces: while a task with the same (kind, key) is in flight, a
// second submit is a no-op counted as "coalesced". An empty key disables
// coalescing for that call.
//
// Returns the outcome it recorded, so callers and tests can assert on it
// without scraping Prometheus.
func (p *workPool) submit(kind workKind, key string, fn func()) string {
	if p == nil {
		// Unwired pool (older tests): preserve the pre-ADR-191
		// behaviour of running inline rather than dropping work.
		fn()
		return "inline"
	}
	if key != "" {
		p.mu.Lock()
		if _, exists := p.inFlight[kind][key]; exists {
			p.mu.Unlock()
			p.observe(kind, "coalesced")
			if p.log != nil {
				p.log.Debug("sched: duplicate loop work coalesced", "kind", string(kind), "key", key)
			}
			return "coalesced"
		}
		p.inFlight[kind][key] = struct{}{}
		p.mu.Unlock()
	}

	run := func() {
		started := time.Now()
		defer func() {
			if key != "" {
				p.mu.Lock()
				delete(p.inFlight[kind], key)
				p.mu.Unlock()
			}
			// A panicking task must not take the daemon down or leak
			// the slot: the loop is the only thing keeping this node
			// scheduling. Recover, log, and let the next tick retry.
			if r := recover(); r != nil {
				p.observe(kind, "panicked")
				if p.log != nil {
					p.log.Error("sched: loop work panicked", "kind", string(kind), "key", key, "panic", r)
				}
			}
			p.observeDuration(kind, time.Since(started))
		}()
		fn()
	}

	slots := p.slots[kind]
	select {
	case slots <- struct{}{}:
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() { <-slots }()
			run()
		}()
		p.observe(kind, "queued")
		return "queued"
	default:
	}

	if workSpecs[kind].overflow == overflowInline {
		if p.log != nil {
			p.log.Warn("sched: loop work slots saturated; running inline",
				"kind", string(kind), "key", key, "slots", workSpecs[kind].slots)
		}
		p.observe(kind, "inline")
		run()
		return "inline"
	}
	if p.log != nil {
		p.log.Warn("sched: loop work slots saturated; dropped (durable source will retry)",
			"kind", string(kind), "key", key, "slots", workSpecs[kind].slots)
	}
	// Release the coalescing key: the task never ran, so a later
	// submit for the same key must not be swallowed as a duplicate.
	if key != "" {
		p.mu.Lock()
		delete(p.inFlight[kind], key)
		p.mu.Unlock()
	}
	p.observe(kind, "dropped")
	return "dropped"
}

// drain blocks until every dispatched task has returned. Production never
// calls it — the loop is never "done". Tests use it to join the workers
// before asserting on rows.
func (p *workPool) drain() {
	if p == nil {
		return
	}
	p.wg.Wait()
}

func (p *workPool) observe(kind workKind, outcome string) {
	if p == nil || p.ops == nil {
		return
	}
	p.ops.LoopWork(string(kind), outcome).Inc()
}

func (p *workPool) observeDuration(kind workKind, d time.Duration) {
	if p == nil || p.ops == nil {
		return
	}
	p.ops.ObserveLoopWorkDuration(string(kind), d.Seconds())
}
