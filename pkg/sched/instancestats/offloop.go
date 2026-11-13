package instancestats

import (
	"context"
	"sync"
	"time"
)

// The poller's tick runs on schedd's select loop every 200 ms. Its
// enforcement actions and the quiet-pause ledger's database calls used to run
// inside it: a disk-pressure or egress-abuse recycle takes the app lock (held
// through a waking instance's boot) and waits up to 5 s on a destroy, and a
// ledger load or flush waits up to 2 s on Postgres. They now leave the tick
// (ADR 20261104143005).

// EnforcementDispatch runs fn off the tick, at most once at a time per key.
// It may drop fn when its pool is full: the action is then not recorded as
// taken, so the next tick that still sees the condition submits it again.
type EnforcementDispatch func(key string, fn func())

// WithEnforcementDispatch sends disk-pressure, egress-abuse and CPU
// saturation actions through dispatch instead of running them on the tick.
func (p *Poller) WithEnforcementDispatch(dispatch EnforcementDispatch) *Poller {
	if p != nil {
		p.enforce = dispatch
	}
	return p
}

// runEnforcement runs fn through the dispatch, or inline without one.
func (p *Poller) runEnforcement(key string, fn func()) {
	if p.enforce != nil {
		p.enforce(key, fn)
		return
	}
	fn()
}

// pauseLedgerQueueDepth is how many node reports may wait for the ledger. A
// fuller queue makes the tick wait, so reports are never dropped or
// reordered: the ledger skips a report older than one it has applied.
const pauseLedgerQueueDepth = 64

type pauseReport struct {
	rows []InstanceStat
	now  time.Time
}

// pauseQueue feeds the ledger's single worker.
type pauseQueue struct {
	once    sync.Once
	reports chan pauseReport
	pending sync.WaitGroup
}

// observePauses hands one tick's rows to the ledger's worker, in tick order.
func (p *Poller) observePauses(ctx context.Context, rows []InstanceStat, now time.Time) {
	if p == nil || p.PauseLedger == nil {
		return
	}
	q := &p.pauses
	q.once.Do(func() {
		q.reports = make(chan pauseReport, pauseLedgerQueueDepth)
		ledger := p.PauseLedger
		// Billing depends on these writes, so the worker outlives a
		// cancelled tick; it stops with the process.
		workerCtx := context.WithoutCancel(ctx)
		go func() {
			for r := range q.reports {
				ledger.Observe(workerCtx, r.rows, r.now)
				q.pending.Done()
			}
		}()
	})
	q.pending.Add(1)
	select {
	case q.reports <- pauseReport{rows: rows, now: now}:
	case <-ctx.Done():
		q.pending.Done()
	}
}

// waitPauses blocks until every queued report reached the ledger.
func (p *Poller) waitPauses() {
	p.pauses.pending.Wait()
}
