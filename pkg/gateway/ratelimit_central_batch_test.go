// ratelimit_central_batch_test.go pins consult coalescing for central rate limits.
// adr: 104
package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// batchingCentral is a CentralBatchBackend double with a token balance. The
// single-token consume can be held open so tests control when the leader's
// statement finishes, and it records the peak number of concurrent statements.
type batchingCentral struct {
	mu        sync.Mutex
	tokens    int
	batchErr  error
	batchSize []int

	hold        chan struct{}
	batchHold   chan struct{}
	leaderStart chan struct{}

	active     atomic.Int64
	peakActive atomic.Int64
	singles    atomic.Int64
}

func (b *batchingCentral) enter() func() {
	n := b.active.Add(1)
	for {
		peak := b.peakActive.Load()
		if n <= peak || b.peakActive.CompareAndSwap(peak, n) {
			break
		}
	}
	return func() { b.active.Add(-1) }
}

func (b *batchingCentral) ConsumeToken(ctx context.Context, _, _, _ string, _, _ float64) (int, bool, error) {
	defer b.enter()()
	b.singles.Add(1)
	if b.leaderStart != nil {
		close(b.leaderStart)
		b.leaderStart = nil
	}
	if b.hold != nil {
		select {
		case <-b.hold:
		case <-ctx.Done():
			return 0, false, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens < 1 {
		return 0, false, nil
	}
	b.tokens--
	return b.tokens, true, nil
}

func (b *batchingCentral) ConsumeTokens(ctx context.Context, _, _, _ string, _, _ float64, n int) (int, int, error) {
	defer b.enter()()
	if b.batchHold != nil {
		select {
		case <-b.batchHold:
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.batchSize = append(b.batchSize, n)
	if b.batchErr != nil {
		return 0, 0, b.batchErr
	}
	granted := min(n, b.tokens)
	b.tokens -= granted
	return granted, b.tokens, nil
}

func (b *batchingCentral) PeekToken(context.Context, string, string, string) (int, error) {
	return 0, nil
}

func (b *batchingCentral) Invalidate(string, string, string) {}

// joinBehindLeader starts a leader consult that blocks inside the backend,
// then n followers, and returns once every follower has joined the batch.
func joinBehindLeader(t *testing.T, c *centralCoalescer, backend *batchingCentral, n int) (leader chan bool, followers chan centralResult) {
	t.Helper()
	backend.hold = make(chan struct{})
	backend.leaderStart = make(chan struct{})
	started := backend.leaderStart
	leader = make(chan bool, 1)
	go func() {
		_, admitted, _ := c.consume(context.Background(), backend, backend, "app", "subject", "scale", 100, 100)
		leader <- admitted
	}()
	<-started
	followers = make(chan centralResult, n)
	for range n {
		go func() {
			remaining, admitted, err := c.consume(context.Background(), backend, backend, "app", "subject", "scale", 100, 100)
			followers <- centralResult{remaining: remaining, admitted: admitted, err: err}
		}()
	}
	key := centralBatchKey{scope: "app", subjectID: "subject", plan: "scale"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		joined := 0
		if next := c.inflight[key]; next != nil {
			joined = next.n
		}
		c.mu.Unlock()
		if joined == n {
			return leader, followers
		}
		if time.Now().After(deadline) {
			t.Fatalf("followers joined = %d, want %d", joined, n)
		}
		time.Sleep(time.Millisecond)
	}
}

type centralResult struct {
	remaining int
	admitted  bool
	err       error
}

// TestCentralCoalescer_ChargesQueuedRequestsInOneStatement is the pool fix:
// 40 requests that arrive while one consult is in flight cost one more
// statement, not 40, and never more than one statement runs at a time.
func TestCentralCoalescer_ChargesQueuedRequestsInOneStatement(t *testing.T) {
	backend := &batchingCentral{tokens: 31}
	var c centralCoalescer
	leader, followers := joinBehindLeader(t, &c, backend, 40)
	close(backend.hold)

	if !<-leader {
		t.Fatal("leader rejected with tokens available")
	}
	admitted, rejected := 0, 0
	remainings := map[int]bool{}
	for range 40 {
		got := <-followers
		if got.err != nil {
			t.Fatalf("follower error: %v", got.err)
		}
		if got.admitted {
			admitted++
			remainings[got.remaining] = true
		} else {
			rejected++
		}
	}
	// 31 tokens: one for the leader, 30 of the 40 followers.
	if admitted != 30 || rejected != 10 {
		t.Fatalf("followers admitted/rejected = %d/%d, want 30/10", admitted, rejected)
	}
	if len(remainings) != 30 || !remainings[0] || !remainings[29] {
		t.Fatalf("admitted followers should see the sequential balances 29..0, got %v", remainings)
	}
	if got := backend.singles.Load(); got != 1 {
		t.Fatalf("single consults = %d, want 1 (the leader)", got)
	}
	if len(backend.batchSize) != 1 || backend.batchSize[0] != 40 {
		t.Fatalf("batched statements = %v, want one of 40", backend.batchSize)
	}
	if peak := backend.peakActive.Load(); peak != 1 {
		t.Fatalf("peak concurrent statements = %d, want 1", peak)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inflight) != 0 {
		t.Fatalf("idle coalescer still tracks %d keys", len(c.inflight))
	}
}

func TestCentralCoalescer_BatchErrorReachesEveryFollower(t *testing.T) {
	backend := &batchingCentral{tokens: 10, batchErr: errors.New("pool exhausted")}
	var c centralCoalescer
	leader, followers := joinBehindLeader(t, &c, backend, 5)
	close(backend.hold)
	<-leader
	for range 5 {
		if got := <-followers; got.err == nil || got.admitted {
			t.Fatalf("follower = %+v, want the batch error", got)
		}
	}
}

// TestCentralCoalescer_FollowerWaitIsBounded keeps a stalled statement from
// holding requests longer than one consult timeout after they join.
func TestCentralCoalescer_FollowerWaitIsBounded(t *testing.T) {
	// The leader has its own timeout. Its expiry starts the follower batch,
	// which must also remain stalled while the follower's timer expires.
	backend := &batchingCentral{tokens: 10, batchHold: make(chan struct{})}
	t.Cleanup(func() { close(backend.batchHold) })
	var c centralCoalescer
	_, followers := joinBehindLeader(t, &c, backend, 1)
	start := time.Now()
	got := <-followers
	if !errors.Is(got.err, errCentralBatchWait) {
		t.Fatalf("follower err = %v, want errCentralBatchWait", got.err)
	}
	if waited := time.Since(start); waited > 2*centralConsultTimeout {
		t.Fatalf("follower waited %s, want about %s", waited, centralConsultTimeout)
	}
	close(backend.hold)
}

// TestLimiter_CoalescedConsultsStillCountCentrally drives the Limiter: a
// batched rejection overrides the local bucket, exactly like a single
// consult does, so coalescing cannot leak extra admissions.
func TestLimiter_CoalescedConsultsStillCountCentrally(t *testing.T) {
	backend := &batchingCentral{tokens: 3}
	l := NewLimiterWithCentral(backend)
	const centralKey = "app:00000000-0000-0000-0000-000000000002:scale"

	backend.hold = make(chan struct{})
	backend.leaderStart = make(chan struct{})
	started := backend.leaderStart
	results := make(chan bool, 6)
	go func() { results <- l.AllowWithCentralParams(t.Context(), "appid", 1500, 3000, centralKey) }()
	<-started
	for range 5 {
		go func() { results <- l.AllowWithCentralParams(t.Context(), "appid", 1500, 3000, centralKey) }()
	}
	key := centralBatchKey{scope: "app", subjectID: "00000000-0000-0000-0000-000000000002", plan: "scale"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.coalescer.mu.Lock()
		joined := 0
		if next := l.coalescer.inflight[key]; next != nil {
			joined = next.n
		}
		l.coalescer.mu.Unlock()
		if joined == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("followers joined = %d, want 5", joined)
		}
		time.Sleep(time.Millisecond)
	}
	close(backend.hold)
	admitted := 0
	for range 6 {
		if <-results {
			admitted++
		}
	}
	if admitted != 3 {
		t.Fatalf("admitted = %d with 3 central tokens, want 3", admitted)
	}
}
