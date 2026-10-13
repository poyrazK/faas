package sched

import (
	"sync"
	"time"
)

// storeSettle remembers since when this process has been able to read the
// store without interruption (ADR-1152 §5 rule 4).
//
// Liveness verdicts that compare a durable heartbeat timestamp with "now"
// are only meaningful once every node has had a full window to write a
// fresh timestamp. After a Postgres outage longer than that window, every
// node's last_heartbeat_at is old — not because the nodes died, but because
// nobody could write. A verdict taken at that moment would mark the whole
// fleet dead and recover or fail every instance at once. The same holds
// when this process starts after a long outage: it has no evidence that
// peers were silent while the store was readable.
//
// Callers report each store read with observe and act only once settled
// returns true for their staleness window.
type storeSettle struct {
	mu    sync.Mutex
	since time.Time
	// waiting records that the caller was told to wait during the current
	// settle period, so it can log once instead of on every tick.
	waiting bool
}

// observe records the outcome of a store read at now. An error restarts the
// settle period; the first success after an error (or at startup) starts it.
func (s *storeSettle) observe(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.since = time.Time{}
		s.waiting = false
		return
	}
	if s.since.IsZero() {
		s.since = now
	}
}

// settled reports whether store reads have succeeded continuously for at
// least window as of now. firstWait is true the first time a caller is
// held back in the current settle period, so it can log the wait once.
func (s *storeSettle) settled(now time.Time, window time.Duration) (ok, firstWait bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.since.IsZero() && now.Sub(s.since) >= window {
		s.waiting = false
		return true, false
	}
	firstWait = !s.waiting
	s.waiting = true
	return false, firstWait
}

// settledSince marks the store readable since t. Tests use it to start from a
// settled process; production code only uses observe.
func (s *storeSettle) settledSince(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.since = t
	s.waiting = false
}
