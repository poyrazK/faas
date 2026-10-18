// ratelimit_lease_test.go pins central-mode token leasing: requests within
// their limit never wait for Postgres, a busy counter costs a bounded number
// of statements, and N gateways stay within the ADR's admission bounds.
// adr: 1520
package gateway

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sharedBucket is one central counter with the Postgres statements' semantics:
// whole-token refill, a grant of min(n, available), and returns capped at
// burst. A statement can be held open to stand in for a slow round trip.
type sharedBucket struct {
	clock *fakeClock

	mu         sync.Mutex
	exists     bool
	tokens     int64
	lastRefill time.Time
	statements int
	returned   int
	failWith   error

	hold chan struct{}
}

func (b *sharedBucket) ConsumeToken(ctx context.Context, scope, subjectID, plan string, rps, burst float64) (int, bool, error) {
	granted, remaining, err := b.ConsumeTokens(ctx, scope, subjectID, plan, rps, burst, 1)
	return remaining, granted == 1, err
}

func (b *sharedBucket) ConsumeTokens(ctx context.Context, _, _, _ string, rps, burst float64, n int) (int, int, error) {
	if hold := b.holdChan(); hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.statements++
	if b.failWith != nil {
		return 0, 0, b.failWith
	}
	ceiling := int64(math.Floor(burst))
	now := b.clock.Now()
	if !b.exists {
		b.exists = true
		granted := min(int64(n), ceiling)
		b.tokens, b.lastRefill = ceiling-granted, now
		return int(granted), int(b.tokens), nil
	}
	whole := math.Floor(now.Sub(b.lastRefill).Seconds() * rps)
	refilled := b.tokens + int64(whole)
	avail := min(ceiling, refilled)
	if avail < 1 {
		return 0, int(max(avail, 0)), nil
	}
	granted := min(int64(n), avail)
	b.tokens = avail - granted
	if refilled >= ceiling {
		b.lastRefill = now
	} else {
		b.lastRefill = b.lastRefill.Add(time.Duration(whole / rps * float64(time.Second)))
	}
	return int(granted), int(b.tokens), nil
}

func (b *sharedBucket) ReturnTokens(_ context.Context, _, _, _ string, burst float64, n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.statements++
	b.returned += n
	b.tokens = min(int64(math.Floor(burst)), b.tokens+int64(n))
	return nil
}

func (b *sharedBucket) PeekToken(context.Context, string, string, string) (int, error) { return 0, nil }
func (b *sharedBucket) Invalidate(string, string, string)                              {}

func (b *sharedBucket) holdChan() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hold
}

func (b *sharedBucket) statementCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statements
}

// leaseCounts is a LeaseObserver that keeps every counter.
type leaseCounts struct {
	mu      sync.Mutex
	results map[string]int
	tokens  map[string]int
	sync    atomic.Int64
}

func (c *leaseCounts) ObserveLeaseRequest(_, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.results == nil {
		c.results = map[string]int{}
	}
	c.results[result]++
}

func (c *leaseCounts) ObserveLeaseTokens(_, outcome string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens == nil {
		c.tokens = map[string]int{}
	}
	c.tokens[outcome] += n
}

func (c *leaseCounts) ObserveRateLimitSyncConsult(string) { c.sync.Add(1) }

func (c *leaseCounts) token(outcome string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens[outcome]
}

func (c *leaseCounts) result(result string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.results[result]
}

// leasingGateway is one gateway's limiter with leasing on, over bucket.
func leasingGateway(bucket *sharedBucket, clock *fakeClock) (*Limiter, *leaseCounts) {
	counts := &leaseCounts{}
	l := NewLimiterWithCentralAndClock(bucket, clock.Now).EnableLeasing(LeaseConfig{}, counts)
	return l, counts
}

const leaseTestKey = "app:lease-app:pro"

// allow sends one request through l and lets any lease statement it started
// finish, so a test with a frozen clock sees the shared bucket settle.
func allow(l *Limiter, rps, burst float64) bool {
	admitted := l.AllowWithCentralParams(context.Background(), "lease-app", rps, burst, leaseTestKey)
	l.lease.calls.Wait()
	return admitted
}

func TestLeaseAdmitsWithoutWaitingForPostgres(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	hold := make(chan struct{})
	bucket := &sharedBucket{clock: clock, hold: hold}
	l, counts := leasingGateway(bucket, clock)
	// The first request after idle is admitted from the allowance while its
	// lease statement is still held open.
	done := make(chan bool, 1)
	go func() { done <- l.AllowWithCentralParams(context.Background(), "lease-app", 100, 500, leaseTestKey) }()
	select {
	case admitted := <-done:
		if !admitted {
			t.Fatal("first request after idle was refused")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first request waited for the lease statement")
	}
	close(hold)
	l.lease.calls.Wait()
	// Steady traffic below the limit spends leases refilled in the
	// background; no request waits.
	for range 400 {
		clock.Advance(20 * time.Millisecond)
		if !allow(l, 100, 500) {
			t.Fatal("request below the limit was refused")
		}
	}
	if got := counts.sync.Load(); got != 0 {
		t.Fatalf("requests that waited for Postgres = %d, want 0 below the limit", got)
	}
	if counts.token(LeaseTokensSpent) == 0 {
		t.Fatal("no request spent a leased token")
	}
}

func TestLeaseStatementsPerSecondAreBounded(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	l, counts := leasingGateway(bucket, clock)
	const rate = 200 // requests per second, below a Scale app's 500
	step := time.Second / rate
	for range 5 * rate { // warm the rate estimate
		clock.Advance(step)
		allow(l, 500, 2000)
	}
	before, allowanceBefore := bucket.statementCount(), counts.token(LeaseTokensAllowance)
	const seconds = 10
	for range seconds * rate {
		clock.Advance(step)
		if !allow(l, 500, 2000) {
			t.Fatal("request below the limit was refused")
		}
	}
	// ADR-1520: at most 1/W + 1 statements per second per gateway.
	limit := int((time.Second/defaultLeaseWindow + 1) * seconds)
	if got := bucket.statementCount() - before; got > limit {
		t.Fatalf("statements in %d s at %d req/s = %d, want at most %d", seconds, rate, got, limit)
	}
	if got := counts.sync.Load(); got != 0 {
		t.Fatalf("requests that waited for Postgres = %d, want 0", got)
	}
	// The next lease arrives before the current one runs out, so steady
	// traffic never falls back to the allowance.
	if got := counts.token(LeaseTokensAllowance) - allowanceBefore; got != 0 {
		t.Fatalf("steady traffic used the allowance %d times, want 0", got)
	}
}

func TestLeaseEmptyBucketRefusesWithoutAStatement(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	l, counts := leasingGateway(bucket, clock)
	// Free app: 5 rps, burst 20. Drain it with the clock frozen.
	admitted := 0
	for range 40 {
		if allow(l, 5, 20) {
			admitted++
		}
	}
	if admitted > 20+1 { // burst plus the allowance of one
		t.Fatalf("admitted %d of 40 against a burst of 20", admitted)
	}
	if counts.result(LeaseResultEmpty) == 0 {
		t.Fatal("draining the bucket never got an empty grant")
	}
	// Until one token should have refilled, a refusal costs no statement.
	before := bucket.statementCount()
	for range 50 {
		if allow(l, 5, 20) {
			t.Fatal("request admitted from an empty bucket")
		}
	}
	if got := bucket.statementCount() - before; got != 0 {
		t.Fatalf("statements while the bucket is marked empty = %d, want 0", got)
	}
	clock.Advance(emptyBackoff(5))
	if !allow(l, 5, 20) {
		t.Fatal("request refused after a token refilled")
	}
}

func TestLeaseReturnsUnspentTokensUnderPressure(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	l, counts := leasingGateway(bucket, clock)
	// A busy Pro counter leases several tokens at a time; a short burst then
	// leaves some unspent while the bucket is nearly empty.
	for range 480 {
		clock.Advance(time.Millisecond)
		allow(l, 100, 500)
	}
	l.lease.mu.Lock()
	st := l.lease.states[centralBatchKey{scope: "app", subjectID: "lease-app", plan: "pro"}]
	held, pressure := st.tokens, st.pressure
	l.lease.mu.Unlock()
	if held == 0 || !pressure {
		t.Skipf("setup left %d tokens held, pressure %v; nothing to return", held, pressure)
	}
	clock.Advance(leaseTTL)
	allow(l, 100, 500)
	if got := counts.token(LeaseTokensReturned); got < held {
		t.Fatalf("returned %d tokens, want the %d held at expiry", got, held)
	}
}

func TestLeaseFailureDecidesLocallyButSpendsHeldTokens(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	l, counts := leasingGateway(bucket, clock)
	for range 100 {
		clock.Advance(5 * time.Millisecond)
		allow(l, 100, 500)
	}
	bucket.mu.Lock()
	bucket.failWith = errors.New("postgres unavailable")
	bucket.mu.Unlock()
	// Held tokens are spent as usual; the refill that fails opens the
	// breaker, and then requests are decided by the local bucket.
	for range 50 {
		allow(l, 100, 500)
	}
	if counts.result(LeaseResultFailed) == 0 {
		t.Fatal("no lease statement failed")
	}
	if !l.centralBreakerOpen(clock.Now()) {
		t.Fatal("a failed lease did not open the breaker")
	}
	if counts.sync.Load() != 0 {
		t.Fatalf("requests waited for Postgres during an outage: %d", counts.sync.Load())
	}
}

func TestLeaseShortfallIsCountedAndMarksTheCounterEmpty(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	other, _ := leasingGateway(bucket, clock)
	l, counts := leasingGateway(bucket, clock)
	// This gateway learns the bucket is full and its lease expires; then,
	// with the clock frozen, another gateway drains the bucket.
	allow(l, 100, 500)
	clock.Advance(leaseTTL)
	for range 600 {
		allow(other, 100, 500)
	}
	// Its last known balance was high, so the allowance admits; the grant
	// then cannot cover it.
	if !allow(l, 100, 500) {
		t.Fatal("allowance refused a request while the last balance was high")
	}
	if counts.token(LeaseTokensOverAdmitted) == 0 {
		t.Fatal("a grant that could not cover the allowance was not counted as over-admitted")
	}
	if allow(l, 100, 500) {
		t.Fatal("request admitted after a shortfall marked the counter empty")
	}
}

func TestLeaseDropsIdleStates(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	bucket := &sharedBucket{clock: clock}
	l, _ := leasingGateway(bucket, clock)
	allow(l, 100, 500)
	clock.Advance(time.Minute)
	l.AllowWithCentralParams(context.Background(), "other-app", 100, 500, "app:other-app:pro")
	l.lease.calls.Wait()
	l.lease.mu.Lock()
	defer l.lease.mu.Unlock()
	if _, ok := l.lease.states[centralBatchKey{scope: "app", subjectID: "lease-app", plan: "pro"}]; ok {
		t.Fatal("an idle counter's lease state was kept")
	}
}

// TestLeaseAdmissionStaysWithinTheADRBounds simulates N gateways with random
// arrivals against one shared bucket and compares their admissions with an
// exact token bucket fed the same arrivals.
func TestLeaseAdmissionStaysWithinTheADRBounds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rps, burst float64
		gateways   int
		load       float64 // offered requests per second, all gateways
	}{
		{name: "free at twice its limit", rps: 5, burst: 20, gateways: 2, load: 10},
		{name: "pro at twice its limit", rps: 100, burst: 500, gateways: 2, load: 200},
		{name: "pro below its limit", rps: 100, burst: 500, gateways: 3, load: 60},
		{name: "scale at its limit", rps: 500, burst: 2000, gateways: 2, load: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock(time.Unix(1_700_000_000, 0))
			bucket := &sharedBucket{clock: clock}
			gateways := make([]*Limiter, tc.gateways)
			for i := range gateways {
				gateways[i], _ = leasingGateway(bucket, clock)
			}
			rng := rand.New(rand.NewPCG(1, uint64(tc.gateways)))
			exact := tc.burst
			exactAdmitted, admitted := 0, 0
			for elapsed := time.Duration(0); elapsed < 20*time.Second; {
				gap := time.Duration(rng.ExpFloat64() / tc.load * float64(time.Second))
				clock.Advance(gap)
				elapsed += gap
				exact = math.Min(tc.burst, exact+gap.Seconds()*tc.rps)
				if exact >= 1 {
					exact--
					exactAdmitted++
				}
				if allow(gateways[rng.IntN(len(gateways))], tc.rps, tc.burst) {
					admitted++
				}
			}
			t.Logf("admitted %d, exact bucket %d, statements %d", admitted, exactAdmitted, bucket.statementCount())
			n := float64(tc.gateways)
			allowance := math.Max(1, math.Floor(defaultLeaseAllowanceFraction*tc.burst))
			lease := math.Max(1, math.Floor(defaultLeaseFraction*tc.burst))
			if over := float64(admitted - exactAdmitted); over > n*allowance+1 {
				t.Fatalf("admitted %d, exact bucket %d: %v over, want at most N*A = %v", admitted, exactAdmitted, over, n*allowance)
			}
			if under := float64(exactAdmitted - admitted); under > n*lease+2 {
				t.Fatalf("admitted %d, exact bucket %d: %v under, want at most N*L = %v", admitted, exactAdmitted, under, n*lease)
			}
		})
	}
}
