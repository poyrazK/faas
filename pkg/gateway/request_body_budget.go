package gateway

import (
	"net/http"
	"sync"
	"sync/atomic"
)

// requestBodyBudget bounds the request-body bytes one gateway holds at once.
// Without it a handful of concurrent large uploads to any public app could
// fill the spool volume or, for bodies held in the heap, push the daemon past
// its memory cap and take every app on the node down with it.
type requestBodyBudget struct {
	maxBytes     int64
	perClientMax int // 0 = no per-client cap
	used         atomic.Int64

	mu        sync.Mutex
	perClient map[string]int
}

func newRequestBodyBudget(maxBytes int64, perClientMax int) *requestBodyBudget {
	return &requestBodyBudget{maxBytes: maxBytes, perClientMax: perClientMax, perClient: map[string]int{}}
}

// lease opens a hold for one request, taking one of its client's slots.
// A nil budget hands out leases that never refuse.
func (b *requestBodyBudget) lease(client string) (*requestBodyLease, bool) {
	if b == nil {
		return &requestBodyLease{}, true
	}
	if b.perClientMax > 0 && client != "" {
		b.mu.Lock()
		if b.perClient[client] >= b.perClientMax {
			b.mu.Unlock()
			return nil, false
		}
		b.perClient[client]++
		b.mu.Unlock()
	}
	return &requestBodyLease{budget: b, client: client}, true
}

func (b *requestBodyBudget) reserve(n int64) bool {
	for {
		cur := b.used.Load()
		if cur+n > b.maxBytes {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

// requestBodyLease is one request's share of a requestBodyBudget. release is
// idempotent, so every exit path may call it.
type requestBodyLease struct {
	budget *requestBodyBudget
	client string
	bytes  int64
	once   sync.Once
}

// grow takes n more bytes; false when the budget is spent.
func (l *requestBodyLease) grow(n int64) bool {
	if l.budget == nil || n <= 0 {
		return true
	}
	if !l.budget.reserve(n) {
		return false
	}
	l.bytes += n
	return true
}

// shrink hands back bytes reserved but not used.
func (l *requestBodyLease) shrink(n int64) {
	if l.budget == nil || n <= 0 || n > l.bytes {
		return
	}
	l.bytes -= n
	l.budget.used.Add(-n)
}

func (l *requestBodyLease) release() {
	if l == nil || l.budget == nil {
		return
	}
	l.once.Do(func() {
		l.budget.used.Add(-l.bytes)
		if l.budget.perClientMax > 0 && l.client != "" {
			l.budget.mu.Lock()
			if l.budget.perClient[l.client] <= 1 {
				delete(l.budget.perClient, l.client)
			} else {
				l.budget.perClient[l.client]--
			}
			l.budget.mu.Unlock()
		}
	})
}

// requestBodyClient is the per-client key for body budgets: the trusted
// client address (an IPv6 /64), or "" when the hop did not establish one.
func requestBodyClient(r *http.Request) string {
	ip, ok := TrustedClientIP(r)
	if !ok {
		return ""
	}
	return throttleIPKey(ip)
}
