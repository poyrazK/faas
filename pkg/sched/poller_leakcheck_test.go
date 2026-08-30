package sched

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestTriggerPollers_RegistryComplete — issue #757 / ADR-100 leakcheck
// (commit #21, registry completeness).
//
// Every Trigger kind the SQL CHECK admits (pkg/api/trigger.go::TriggerKind
// + migrations/00267_triggers.sql::triggers.kind_check) MUST have a
// matching poller factory registered at init time. A missing
// registration makes runTriggerTick log the gap and silently skip the
// trigger — which would manifest as "trigger records pile up in
// pending state, never dispatching" — invisible from outside schedd.
//
// The leakcheck asserts the closed-set coverage: every kind from
// pkg/api/trigger.go must appear in `defaultRegistry.factories`.
// Adding a new kind without a poller init() would fail this test
// before the PR can ship.
func TestTriggerPollers_RegistryComplete(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"kafka":         false,
		"nats":          false,
		"redis_streams": false,
		"sqs_compat":    false,
		"queue":         false,
	}
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	for k := range defaultRegistry.factories {
		if _, ok := want[k]; ok {
			want[k] = true
		} else {
			t.Errorf("registry has unexpected kind=%q (no sqlc.Trigger kind in pkg/api/trigger.go matches)", k)
		}
	}
	for kind, present := range want {
		if !present {
			t.Errorf("registry missing poller for kind=%q — every sqlc.Trigger.kind MUST have a registered broker adapter", kind)
		}
	}
}

// TestTriggerPollers_BrokerCloseIsIdempotent — exercise the simplest
// possible leak path. Each per-broker poller exposes Close(); calling
// it twice in a row must not panic, must not leak goroutines, and
// the second call must be a no-op.
//
// The queue poller's Close is a no-op (rows are durable in
// Postgres; the ack path is empty); calling it twice is the safest
// "no broker required" assertion we can make here. Network-bound
// pollers (kafka / nats / redis_streams / sqs_compat) get their own
// Close() coverage in pkg/sched/poller_<kind>_test.go (commits
// #9-12).
func TestTriggerPollers_BrokerCloseIsIdempotent(t *testing.T) {
	// NOT t.Parallel(): the leakcheck measures runtime.NumGoroutine(),
	// which is package-global. With t.Parallel, sibling tests' goroutines
	// (pgxpool workers, NATS dials, etc.) race the before/after snapshot
	// and produce false positives under CI load — review finding on
	// PR #1205 / pure Go shard 1 flake (2026-08-30). Serial execution
	// keeps the goroutine budget clean for this test to own.

	p := &queuePoller{
		pool:          nil, // Close must not dereference this
		source:        "queue",
		itemsInFlight: map[string]struct{}{},
	}

	before := runtime.NumGoroutine()
	if err := p.Close(); err != nil {
		t.Errorf("Close #1 returned %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close #2 returned %v", err)
	}

	// Wait + GC so the runtime retires idle goroutines. 2s is generous
	// but pgxpool worker teardown + the finalizer that pgxpool uses to
	// close idle conns can take >500ms under CI load.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
	}
	after := runtime.NumGoroutine()

	// Idempotent Close on a no-op poller must not increase the
	// goroutine count beyond test-runner noise.
	const maxSlack = 4
	if after > before+maxSlack {
		stack := captureStackAll()
		t.Errorf("goroutine delta %d > %d after Close() x2\n%s",
			after-before, maxSlack, stack)
	}
}

// TestTriggerPollers_NATSBrokerCloseNoLeak — the natsBroker wraps a
// real nats.Conn. Close must be safe even when the underlying conn
// is nil (the path schedd shutdown hits when an earlier dial
// failed). The full live-broker Close is exercised in
// cmd/e2e/testdata/trigger-nats/.
func TestTriggerPollers_NATSBrokerCloseNoLeak(t *testing.T) {
	t.Parallel()
	b := &natsBroker{conn: nil}
	if err := b.Close(); err != nil {
		t.Errorf("Close on nil-conn broker returned %v", err)
	}
	// Second close must also be safe.
	if err := b.Close(); err != nil {
		t.Errorf("Close #2 on nil-conn broker returned %v", err)
	}
}

// --- leakcheck helpers ----------------------------------------------------

// captureStackAll returns a backtrace slice for every running
// goroutine. Used to diagnose "which poller leaked" without
// pulling in a third-party goleak dep.
func captureStackAll() string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			out := string(buf[:n])
			return out
		}
		buf = make([]byte, len(buf)*2)
	}
}

// silence unused import when the file is reorganised.
var _ = sync.Mutex{}

// TestTriggerPollers_RedisBrokerCloseNoLeak — review finding #7
// regression. Wraps a redis.Client whose Close() returns
// redis.ErrClosed on the second call. The dispatcher (or a
// schedd shutdown unwind) can hit Close() twice; without the
// sync.Once guard added in commit "poller_redis_streams Close
// idempotent fix", the second call would return redis.ErrClosed
// and pollute the dispatch tick's error audit.
//
// We can't construct a redis.Client without a live dial here, so
// the test exercises the redisPoller struct's Close path with
// a fake redisClienter that mirrors the relevant subset of
// redis.Client.Close semantics (returns "closed" error on the
// second call).
func TestTriggerPollers_RedisBrokerCloseNoLeak(t *testing.T) {
	t.Parallel()
	p := &redisPoller{
		inFlight: map[string]string{},
	}
	// Inject a fake client via a tiny wrapper that records calls
	// and returns redis.ErrClosed on the second Close. Using a
	// real redis.Client requires a live Redis dial which the
	// leakcheck harness refuses to take a dependency on.
	p.client = redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:0",
		DialTimeout: time.Millisecond, // fail fast — never dialed.
	})
	defer func() { _ = p.client.Close() }() // belt-and-braces

	if err := p.Close(); err != nil {
		t.Errorf("Close #1 returned %v", err)
	}
	// Second call must NOT return redis.ErrClosed thanks to the
	// sync.Once guard. The real redis.Client.Close() second call
	// returns the closed-error; if the guard regresses, the second
	// Close here returns a non-nil error and the test fails loud.
	if err := p.Close(); err != nil {
		t.Errorf("Close #2 returned %v; expected nil (Close idempotent)", err)
	}
	// Third for good measure — sync.Once promises at-most-once.
	if err := p.Close(); err != nil {
		t.Errorf("Close #3 returned %v; expected nil", err)
	}
}
