// Package clientlimit bounds what one client address may take from an edge
// that serves clients directly, with no proxy in front (ADR-20261103163954
// §4): open connections, new connections a second, TLS handshakes in
// progress and requests a second. State lives in memory, per node; nothing
// here reads Postgres.
package clientlimit

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// Key is the address a limit counts against: the IPv4 address itself, or the
// /64 an IPv6 address sits in, since one client commonly holds a whole /64.
func Key(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32)
	}
	p, _ := addr.Prefix(64)
	return p
}

// keyOf is Key for a connection's remote address.
func keyOf(addr net.Addr) (netip.Prefix, bool) {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		if a, ok := netip.AddrFromSlice(tcp.IP); ok {
			return Key(a), true
		}
		return netip.Prefix{}, false
	}
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.Prefix{}, false
	}
	return Key(ap.Addr()), true
}

// sweepInterval is how often a table drops idle addresses while it is below
// its bound.
const sweepInterval = time.Minute

// entry is one address's state: a token bucket and its open connections.
type entry struct {
	tokens float64
	last   time.Time
	open   int
}

// table keeps per-key token buckets (a client address, or a hostname),
// bounded to max keys. When it
// is full and holds no idle address, every further address shares one
// overflow bucket, so a flood from more addresses than the bound is still
// metered and memory stays flat.
type table[K comparable] struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	max       int
	now       func() time.Time
	entries   map[K]*entry
	overflow  entry
	lastSweep time.Time
}

func newTable[K comparable](rate float64, burst, max int, now func() time.Time) *table[K] {
	if now == nil {
		now = time.Now
	}
	t := &table[K]{rate: rate, burst: float64(burst), max: max, now: now, entries: map[K]*entry{}}
	t.overflow.tokens = t.burst
	t.lastSweep = now()
	return t
}

// lookup returns key's entry with its tokens refilled to now, creating it
// when there is room. It reports false for the shared overflow entry. The
// caller holds t.mu.
func (t *table[K]) lookup(key K, now time.Time) (*entry, bool) {
	if now.Sub(t.lastSweep) >= sweepInterval {
		t.sweep(now)
	}
	e, ok := t.entries[key]
	if !ok {
		if len(t.entries) >= t.max && now.Sub(t.lastSweep) >= time.Second {
			t.sweep(now)
		}
		if len(t.entries) >= t.max {
			t.refill(&t.overflow, now)
			return &t.overflow, false
		}
		e = &entry{tokens: t.burst, last: now}
		t.entries[key] = e
	}
	t.refill(e, now)
	return e, true
}

func (t *table[K]) refill(e *entry, now time.Time) {
	if elapsed := now.Sub(e.last).Seconds(); elapsed > 0 {
		e.tokens = min(t.burst, e.tokens+elapsed*t.rate)
	}
	e.last = now
}

// sweep drops addresses with nothing open and a full bucket: forgetting them
// changes nothing. The caller holds t.mu.
func (t *table[K]) sweep(now time.Time) {
	for key, e := range t.entries {
		t.refill(e, now)
		if e.open == 0 && e.tokens >= t.burst {
			delete(t.entries, key)
		}
	}
	t.lastSweep = now
}

// take spends one token from key's bucket. It reports whether one was left
// and how many remain.
func (t *table[K]) take(key K) (bool, float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, _ := t.lookup(key, t.now())
	if e.tokens < 1 {
		return false, e.tokens
	}
	e.tokens--
	return true, e.tokens
}

// size is the number of addresses tracked, for tests.
func (t *table[K]) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}
