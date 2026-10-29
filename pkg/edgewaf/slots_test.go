package edgewaf

import (
	"testing"
	"time"
)

// adr: 831 amendment 16 — the pool lends every free slot to whoever asks,
// and a freed slot goes to an account below its share before one above it.
func TestSlotPoolServesAccountsBelowTheirShareFirst(t *testing.T) {
	p := newSlotPool(4, 50) // share 2
	for range 4 {
		if ok, _ := p.acquire("flood", 0); !ok {
			t.Fatal("a free slot was refused to the only account using the pool")
		}
	}
	if ok, over := p.acquire("flood", 0); ok || !over {
		t.Fatalf("full pool, no wait: ok=%v over=%v, want refused as over the share", ok, over)
	}
	if ok, over := p.acquire("quiet", 0); ok || over {
		t.Fatalf("full pool, no wait, account holding nothing: ok=%v over=%v, want refused as busy", ok, over)
	}

	got := make(chan string, 4)
	wait := func(tenant string) {
		if ok, _ := p.acquire(tenant, 10*time.Second); ok {
			got <- tenant
		}
	}
	go wait("flood")
	waitForWaiters(t, p, 0, 1)
	go wait("a")
	waitForWaiters(t, p, 1, 1)
	go wait("b")
	waitForWaiters(t, p, 2, 1)

	for _, want := range []string{"a", "b", "flood"} {
		p.release("flood")
		if tenant := <-got; tenant != want {
			t.Fatalf("freed slot went to %q, want %q", tenant, want)
		}
	}
	for _, tenant := range []string{"flood", "flood", "a", "b"} {
		p.release(tenant)
	}
	assertPoolIdle(t, p)
}

// A waiter that times out leaves the queue and its account's count, so a
// later release neither hands it a slot nor leaks one.
func TestSlotPoolTimeoutLeavesNoTrace(t *testing.T) {
	p := newSlotPool(2, 50) // share 1
	p.acquire("x", 0)
	p.acquire("x", 0)
	if ok, over := p.acquire("y", time.Millisecond); ok || over {
		t.Fatalf("within-share waiter on a held pool: ok=%v over=%v, want busy", ok, over)
	}
	if ok, over := p.acquire("x", time.Millisecond); ok || !over {
		t.Fatalf("over-share waiter on a held pool: ok=%v over=%v, want over its share", ok, over)
	}
	waitForWaiters(t, p, 0, 0)
	p.release("x")
	p.release("x")
	assertPoolIdle(t, p)
	// A second account below its share is queued within it until it holds
	// its share, then over it.
	p.acquire("x", 0)
	p.acquire("x", 0)
	go p.acquire("y", 10*time.Second)
	waitForWaiters(t, p, 1, 0)
	go p.acquire("y", 10*time.Second)
	waitForWaiters(t, p, 1, 1)
	p.release("x")
	p.release("x")
	p.release("y")
	p.release("y")
	assertPoolIdle(t, p)
}

func assertPoolIdle(t *testing.T, p *slotPool) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy != 0 || len(p.held) != 0 || len(p.queued) != 0 || len(p.within) != 0 || len(p.over) != 0 {
		t.Fatalf("pool not idle: busy=%d held=%v queued=%v within=%d over=%d",
			p.busy, p.held, p.queued, len(p.within), len(p.over))
	}
}
