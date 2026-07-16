// Per-instance last-seen timestamps (spec §4.1). The gateway records the
// last_request_at for the proxied instance every request; schedd's
// pkg/sched/reaper.go reads these to decide when to park an idle app. Spec
// §4.1 wants these flushed to Postgres every 15 s, not on every request —
// the gateway-side batch keeps the request hot path off the DB.
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
// request for an instance address. Implementations are expected to be
// safe for concurrent use.
type LastSeenSink interface {
	// Touch records the timestamp for addr (must be in host:port form as
	// returned by Backend.Target).
	Touch(addr string, t time.Time)
	// Get returns the timestamp and whether it has been recorded.
	Get(addr string) (time.Time, bool)
	// Forget drops addr (e.g. when Target stops returning it).
	Forget(addr string)
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

func (l *MemoryLastSeen) Touch(addr string, t time.Time) {
	l.mu.Lock()
	l.m[addr] = t
	l.mu.Unlock()
}

func (l *MemoryLastSeen) Get(addr string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.m[addr]
	return t, ok
}

func (l *MemoryLastSeen) Forget(addr string) {
	l.mu.Lock()
	delete(l.m, addr)
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
