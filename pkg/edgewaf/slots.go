package edgewaf

import (
	"slices"
	"sync"
	"time"
)

// slotPool is the node's in-path check slots (ADR-831 amendments 12 and 16).
// A check takes a free slot whoever runs it, so one busy account uses the
// whole pool while it is alone. A check that finds every slot busy waits up
// to the slot wait, and a freed slot goes first to a check whose account
// holds less than its share of the pool, then to the rest in arrival order.
// One account's flood of matching requests across its apps therefore cannot
// keep another account's checks waiting until they fail open as busy.
type slotPool struct {
	size  int
	share int

	mu     sync.Mutex
	busy   int
	held   map[string]int // granted slots by tenant
	queued map[string]int // within-share waiters by tenant
	within []*slotWaiter
	over   []*slotWaiter
}

type slotWaiter struct {
	tenant  string
	within  bool
	ready   chan struct{}
	granted bool
}

// newSlotPool returns size slots of which one tenant is served first for
// sharePercent of them (at least one).
func newSlotPool(size, sharePercent int) *slotPool {
	size = max(size, 1)
	return &slotPool{
		size:   size,
		share:  max(size*sharePercent/100, 1),
		held:   map[string]int{},
		queued: map[string]int{},
	}
}

// acquire takes a slot for tenant, waiting up to wait when every slot is
// busy. When it times out, overShare reports that the tenant was waiting
// beyond its share: other tenants were served first.
func (p *slotPool) acquire(tenant string, wait time.Duration) (ok, overShare bool) {
	p.mu.Lock()
	if p.busy < p.size {
		p.grantLocked(tenant)
		p.mu.Unlock()
		return true, false
	}
	if wait <= 0 {
		over := p.held[tenant] >= p.share
		p.mu.Unlock()
		return false, over
	}
	w := &slotWaiter{tenant: tenant, ready: make(chan struct{})}
	if p.held[tenant]+p.queued[tenant] < p.share {
		w.within = true
		p.queued[tenant]++
		p.within = append(p.within, w)
	} else {
		p.over = append(p.over, w)
	}
	p.mu.Unlock()

	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-w.ready:
		return true, false
	case <-t.C:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if w.granted {
		return true, false
	}
	if w.within {
		p.within = slices.DeleteFunc(p.within, func(x *slotWaiter) bool { return x == w })
		p.dequeuedLocked(w)
	} else {
		p.over = slices.DeleteFunc(p.over, func(x *slotWaiter) bool { return x == w })
	}
	return false, !w.within
}

// release frees tenant's slot, handing it straight to the next waiter.
func (p *slotPool) release(tenant string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held[tenant]--; p.held[tenant] <= 0 {
		delete(p.held, tenant)
	}
	p.busy--
	var w *slotWaiter
	switch {
	case len(p.within) > 0:
		w, p.within = p.within[0], p.within[1:]
		p.dequeuedLocked(w)
	case len(p.over) > 0:
		w, p.over = p.over[0], p.over[1:]
	default:
		return
	}
	p.grantLocked(w.tenant)
	w.granted = true
	close(w.ready)
}

func (p *slotPool) grantLocked(tenant string) {
	p.busy++
	p.held[tenant]++
}

func (p *slotPool) dequeuedLocked(w *slotWaiter) {
	if p.queued[w.tenant]--; p.queued[w.tenant] <= 0 {
		delete(p.queued, w.tenant)
	}
}
