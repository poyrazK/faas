package outbound

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// aiDrainSettleWindow is how long before a stopping outboundd's drain grace
// ends it cuts the billable AI calls still running, so each settles at its
// reservation (as unknown usage, ADR-996) before the process exits. A call
// may run 30 minutes and the drain grace is 25 s; without the cut the
// process exited under them, their leases expired unsettled and their spend
// never reached the caps (prod hunt 2026-11-28: outboundd restarted 40
// times in a week).
const aiDrainSettleWindow = 5 * time.Second

// aiDrain tracks a handler's billable AI calls in flight and ends them when
// the daemon stops. The zero value is ready to use.
type aiDrain struct {
	once     sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
	inflight atomic.Int64
	idle     chan struct{}
	mu       sync.Mutex
}

func (d *aiDrain) init() {
	d.once.Do(func() { d.ctx, d.cancel = context.WithCancel(context.Background()) })
}

// track counts call in flight and ends it through its deadline path once
// the drain cuts calls; the returned func, deferred after the call's
// settlement, stops both.
func (d *aiDrain) track(call *aiCallContext) func() {
	d.init()
	d.inflight.Add(1)
	stop := context.AfterFunc(d.ctx, call.expire)
	return func() {
		stop()
		if d.inflight.Add(-1) == 0 {
			d.mu.Lock()
			if d.idle != nil {
				close(d.idle)
				d.idle = nil
			}
			d.mu.Unlock()
		}
	}
}

// waitIdle returns when no call is in flight or done closes; it reports
// whether the calls finished.
func (d *aiDrain) waitIdle(done <-chan struct{}) bool {
	d.mu.Lock()
	if d.inflight.Load() == 0 {
		d.mu.Unlock()
		return true
	}
	if d.idle == nil {
		d.idle = make(chan struct{})
	}
	idle := d.idle
	d.mu.Unlock()
	select {
	case <-idle:
		return true
	case <-done:
		return d.inflight.Load() == 0
	}
}

// EndAICalls ends the billable AI calls in flight, and any started after,
// through their deadline path, so each settles at its reservation. A
// handed-off generation asked to stop early calls it (ADR-20261128150000):
// its drain is then one grace, not the hand-off's half hour.
func (h *Handler) EndAICalls() {
	h.aiDrain.init()
	h.aiDrain.cancel()
}

// DrainAICalls is a stop function for a stopping outboundd: it lets billable
// AI calls finish until aiDrainSettleWindow before ctx's deadline, then
// ends the rest so they settle at their reservation, and returns when they
// have or ctx ends.
func (h *Handler) DrainAICalls(ctx context.Context) {
	d := &h.aiDrain
	d.init()
	cut := ctx.Done()
	if deadline, ok := ctx.Deadline(); ok {
		timer := time.NewTimer(max(time.Until(deadline)-aiDrainSettleWindow, 0))
		defer timer.Stop()
		cutAt := make(chan struct{})
		go func() {
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
			close(cutAt)
		}()
		cut = cutAt
	}
	if d.waitIdle(cut) {
		return
	}
	if h.Log != nil {
		h.Log.Warn("outbound: ending AI calls still running at shutdown; they settle at their reservation",
			"calls", d.inflight.Load())
	}
	d.cancel()
	d.waitIdle(ctx.Done())
}
