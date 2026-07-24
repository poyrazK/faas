// Per-instance last-seen timestamps (spec §4.1). The gateway records the
// last_request_at for the proxied instance every request; schedd's
// pkg/sched/reaper.go reads these to decide when to park an idle app. Spec
// §4.1 wants these flushed to Postgres every 15 s, not on every request —
// the gateway-side batch keeps the request hot path off the DB.
//
// Issue #168: the key is now the instance id (the row PK schedd owns),
// not the dial address. With fan-out across max_concurrency, multiple
// instances can share a single compute_node — last_request_at must be
// attributed per-instance so the reaper's idle budget applies to the
// specific instance the customer was just hitting, not its node-mate
// that has been idle for hours.
//
// Component ownership note: schedd is the ONLY writer to `instances`, so
// `last_request_at` rows are owned by schedd. This package defines the seam
// (LastSeenSink) and ships an in-memory implementation; cmd/schedd wires
// the real PG-flushing implementation in M5 alongside its other instance
// writes. Until then, the in-memory implementation lets the reaper work
// inside a single process for unit tests and the M3 acceptance suite.
package gateway

import (
	"context"
	"sync"
	"time"
)

// LastSeenSink records the last time the gateway saw a successful proxied
// request for an instance. Implementations are expected to be safe for
// concurrent use.
type LastSeenSink interface {
	// Touch records the timestamp for instanceID (the instances.id row PK
	// schedd minted at Wake). Buffered until the next Flush; only the
	// newest per instance id is kept so a burst collapses to one row.
	Touch(instanceID string, t time.Time)
	// Get returns the timestamp and whether it has been recorded.
	Get(instanceID string) (time.Time, bool)
	// Forget drops instanceID (e.g. when the cached target is evicted).
	Forget(instanceID string)
	// Flush drives any buffered writes to durable storage. Called on a
	// 15-second ticker; may be a no-op for the in-memory implementation.
	Flush(ctx context.Context) error
}

// MemoryLastSeen is the in-memory LastSeenSink used by gatewayd until schedd
// lands its PG-flushing implementation. Safe for concurrent use.
type MemoryLastSeen struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func NewMemoryLastSeen() *MemoryLastSeen {
	return &MemoryLastSeen{m: map[string]time.Time{}}
}

func (l *MemoryLastSeen) Touch(instanceID string, t time.Time) {
	l.mu.Lock()
	l.m[instanceID] = t
	l.mu.Unlock()
}

func (l *MemoryLastSeen) Get(instanceID string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.m[instanceID]
	return t, ok
}

func (l *MemoryLastSeen) Forget(instanceID string) {
	l.mu.Lock()
	delete(l.m, instanceID)
	l.mu.Unlock()
}

func (l *MemoryLastSeen) Flush(_ context.Context) error { return nil }

// FlushEvery runs sink.Flush every interval until ctx is cancelled. Errors
// are returned on the returned channel and the loop continues so a flaky
// Postgres doesn't kill the daemon.
func FlushEvery(ctx context.Context, interval time.Duration, sink LastSeenSink) <-chan error {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	errc := make(chan error, 1)
	go func() {
		defer close(errc)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := sink.Flush(ctx); err != nil {
					select {
					case errc <- err:
					default:
					}
				}
			}
		}
	}()
	return errc
}
