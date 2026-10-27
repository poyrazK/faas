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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// workKind is the closed set of off-loop task kinds. Each is a Prometheus
// label value, so the set stays small and fixed.
type workKind string

const (
	workServiceRecovery      workKind = "service_recovery"
	workServiceRecoverySweep workKind = "service_recovery_sweep"
	workPrime                workKind = "prime"
	workRestart              workKind = "restart"
	workAppReconcile         workKind = "app_reconcile"
	workDeploymentReconcile  workKind = "deployment_reconcile"
	workJobCancel            workKind = "job_cancel"
	workJobDispatch          workKind = "job_dispatch"
	workPrimeRecovery        workKind = "prime_recovery"
	workWorkflowDispatch     workKind = "workflow_dispatch"
	workWorkflowSchedules    workKind = "workflow_schedules"
	workTriggerDispatch      workKind = "trigger_dispatch"
	workQueuePushDispatch    workKind = "queue_push_dispatch"
	// workBrokerTriggerDispatch runs one external broker trigger's batch;
	// its key is the trigger, so a trigger has one batch in flight.
	workBrokerTriggerDispatch workKind = "broker_trigger_dispatch"
	workEventFanout           workKind = "event_fanout"
	workReaper                workKind = "reaper"
	workRehearsalFault        workKind = "rehearsal_worker_fault"
	workRehearsalSuspend      workKind = "rehearsal_suspend_resume"
	workRehearsalSweep        workKind = "rehearsal_sweep"
	workWorkerRecovery        workKind = "worker_recovery"
	workWorkerRecoverySweep   workKind = "worker_recovery_sweep"
	// workAdoptionReport applies restarted vmmds' adoption reports.
	workAdoptionReport workKind = "adoption_report"
	// workAppWake and workRuntimeConfigRestart deliver the durable app_wake
	// and runtime_config_restart handoffs, which boot VMs (ADR-191
	// amendment 2026-10-26).
	workAppWake              workKind = "app_wake"
	workRuntimeConfigRestart workKind = "runtime_config_restart"
)

// workKinds is the iteration order for metric pre-instantiation.
var workKinds = []workKind{
	workServiceRecovery, workServiceRecoverySweep,
	workPrime, workRestart, workAppReconcile, workDeploymentReconcile, workJobCancel, workJobDispatch, workPrimeRecovery,
	workWorkflowDispatch, workWorkflowSchedules, workTriggerDispatch, workQueuePushDispatch, workBrokerTriggerDispatch, workEventFanout,
	workReaper, workAdoptionReport, workRehearsalFault, workRehearsalSuspend, workRehearsalSweep, workWorkerRecovery, workWorkerRecoverySweep,
	workAppWake, workRuntimeConfigRestart,
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
//
// rerunCoalesced makes a submit that coalesces into an in-flight task ask
// for one more pass when that task finishes, instead of being dropped. Use
// it where a wake carries news the running pass may already have read past:
// event fanout's event_published arrives while a sweep runs, and without the
// extra pass the new event waits for the safety ticker.
type workSpec struct {
	slots          int
	overflow       overflowPolicy
	rerunCoalesced bool
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
// The fleet-wide prime recovery sweep gets one slot: its constant key
// coalesces ticks, and a dropped tick is retried the next minute. Workflow
// dispatch gets four slots for independent runs; trigger polling and event
// fanout get one slot each because their tick work is already bounded and
// durable state makes the next tick a safe retry. Event fanout reruns once
// after a pass during which an event_published wake arrived.
var workSpecs = map[workKind]workSpec{
	workServiceRecovery:      {slots: api.ServiceRecoveryConcurrentApps, overflow: overflowDrop},
	workServiceRecoverySweep: {slots: 1, overflow: overflowDrop},
	workWorkerRecovery:       {slots: api.WorkerRecoveryConcurrentApps, overflow: overflowDrop},
	workWorkerRecoverySweep:  {slots: 1, overflow: overflowDrop},
	workPrime:                {slots: maxConcurrentPrimes, overflow: overflowInline},
	workRestart:              {slots: 8, overflow: overflowDrop},
	workAppReconcile:         {slots: 8, overflow: overflowDrop},
	workDeploymentReconcile:  {slots: 8, overflow: overflowDrop},
	workJobCancel:            {slots: 8, overflow: overflowDrop},
	workJobDispatch:          {slots: 1, overflow: overflowDrop},
	workPrimeRecovery:        {slots: 1, overflow: overflowDrop},
	workWorkflowDispatch:     {slots: api.WorkflowDispatchSlots, overflow: overflowDrop},
	workWorkflowSchedules:    {slots: 1, overflow: overflowDrop},
	workTriggerDispatch:      {slots: 1, overflow: overflowDrop},
	// Push-queue lanes (ADR-972) claim durable leases; a dropped lane is
	// retried by the next trigger tick.
	workQueuePushDispatch: {slots: api.QueuePushDispatchSlotsPerNode, overflow: overflowDrop},
	// Broker trigger lanes (ADR-20261027162741): a dropped or coalesced
	// lane is resubmitted by the next trigger tick.
	workBrokerTriggerDispatch: {slots: api.BrokerTriggerDispatchSlotsPerNode, overflow: overflowDrop},
	workEventFanout:           {slots: 1, overflow: overflowDrop, rerunCoalesced: true},
	// One reaper tick at a time, sequential inside, as before it moved off
	// the loop. A tick that fires while the previous one still runs
	// coalesces into it; the next tick re-reads the instance table.
	workReaper:           {slots: 1, overflow: overflowDrop},
	workRehearsalFault:   {slots: 1, overflow: overflowDrop},
	workRehearsalSuspend: {slots: 1, overflow: overflowDrop},
	// The rehearsal listing sweep runs off the loop goroutine (ADR-20261019120000);
	// its durable rows make the next trigger tick a safe retry.
	workRehearsalSweep: {slots: 1, overflow: overflowDrop},
	// One pass at a time; a tick during a pass coalesces, and an
	// unapplied report is fetched again on the next tick.
	workAdoptionReport: {slots: 1, overflow: overflowDrop},
	// Explicit wakes and runtime-config restarts each hold a slot for a
	// whole boot. The wake coordinator merges wakes of one app, so the
	// slots bound cross-app fan-out. A dropped delivery stays pending in
	// the notification outbox and its replay worker delivers it after the
	// wakeup grace period.
	workAppWake:              {slots: 8, overflow: overflowDrop},
	workRuntimeConfigRestart: {slots: 4, overflow: overflowDrop},
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
	// rerun holds the latest coalesced submit per in-flight key of a
	// rerunCoalesced kind; the running task takes it when its pass ends.
	rerun   map[workKind]map[string]func()
	closing bool

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
		rerun:    make(map[workKind]map[string]func(), len(workSpecs)),
	}
	for kind, spec := range workSpecs {
		p.slots[kind] = make(chan struct{}, spec.slots)
		p.inFlight[kind] = make(map[string]struct{})
		p.rerun[kind] = make(map[string]func())
	}
	return p
}

// submit runs fn off the caller's goroutine.
//
// key coalesces: while a task with the same (kind, key) is in flight, a
// second submit is a no-op counted as "coalesced", or for a rerunCoalesced
// kind a request for one more pass once the running one ends. An empty key
// disables coalescing for that call.
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
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		p.observe(kind, "dropped")
		return "dropped"
	}
	if key != "" {
		if _, exists := p.inFlight[kind][key]; exists {
			if workSpecs[kind].rerunCoalesced {
				p.rerun[kind][key] = fn
			}
			p.mu.Unlock()
			p.observe(kind, "coalesced")
			if p.log != nil {
				p.log.Debug("sched: duplicate loop work coalesced", "kind", string(kind), "key", key)
			}
			return "coalesced"
		}
		p.inFlight[kind][key] = struct{}{}
	}
	p.mu.Unlock()
	begin := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closing {
			if key != "" {
				delete(p.inFlight[kind], key)
			}
			return false
		}
		// Keep Add under mu so drainFor can close the pool and start Wait
		// without racing a later positive Add.
		p.wg.Add(1)
		return true
	}

	// run executes fn, then any pass a coalesced submit asked for. release
	// frees the task's slot (nil when it runs inline without one). Ending
	// the task drops its key and frees its slot under mu, so a submit sees
	// either a running task to coalesce into or a free slot, never the
	// moment between, where it used to be dropped as saturated.
	run := func(release func()) {
		started := time.Now()
		finished := false
		finish := func() { // caller holds p.mu
			if key != "" {
				delete(p.inFlight[kind], key)
				delete(p.rerun[kind], key)
			}
			if release != nil {
				release()
			}
			finished = true
		}
		defer func() {
			if !finished {
				p.mu.Lock()
				finish()
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
			p.wg.Done()
		}()
		for {
			fn()
			p.mu.Lock()
			next := p.rerun[kind][key]
			if next == nil || p.closing {
				finish()
				p.mu.Unlock()
				return
			}
			delete(p.rerun[kind], key)
			p.mu.Unlock()
			p.observe(kind, "rerun")
			fn = next
		}
	}

	slots := p.slots[kind]
	select {
	case slots <- struct{}{}:
		if !begin() {
			<-slots
			p.observe(kind, "dropped")
			return "dropped"
		}
		go run(func() { <-slots })
		p.observe(kind, "queued")
		return "queued"
	default:
	}

	if workSpecs[kind].overflow == overflowInline {
		if !begin() {
			p.observe(kind, "dropped")
			return "dropped"
		}
		if p.log != nil {
			p.log.Warn("sched: loop work slots saturated; running inline",
				"kind", string(kind), "key", key, "slots", workSpecs[kind].slots)
		}
		p.observe(kind, "inline")
		run(nil)
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

// drainFor stops accepting new work and waits up to timeout for accepted
// tasks to finish. The scheduler calls this after daemon cancellation so
// Prime's bounded VM cleanup can use the still-open database connection.
func (p *workPool) drainFor(timeout time.Duration) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
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
