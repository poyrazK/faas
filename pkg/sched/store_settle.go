package sched

import (
	"sync"
	"time"
)

// storeSettle holds liveness verdicts back after this process has seen the
// store unavailable (ADR-1344 §5 rule 4).
//
// Liveness verdicts that compare a durable heartbeat timestamp with "now"
// are only meaningful once every node has had a full window to write a
// fresh timestamp. After a Postgres outage longer than that window, every
// node's last_heartbeat_at is old — not because the nodes died, but because
// nobody could write. A verdict taken at that moment would mark the whole
// fleet dead and recover or fail every instance at once.
//
// The hold starts on evidence of an outage: a failed store read, or a hold
// call when the process saw the store unavailable before this tracker
// existed (a boot-time wait for the database, or a standby that met store
// errors while waiting for its lease). A process that started against a
// healthy store gives its verdicts at once; holding on every start would
// delay dead-node recovery after each restart and failover with no outage
// to justify it.
//
// Callers report each store read with observe and act only once settled
// returns true for their staleness window.
type storeSettle struct {
	mu sync.Mutex
	// held is set by evidence of an outage and cleared once reads have
	// succeeded without interruption for a full window.
	held bool
	// since is the first successful read of the current settle period.
	since time.Time
	// waiting records that the caller was told to wait during the current
	// settle period, so it can log once instead of on every tick.
	waiting bool
}

// observe records the outcome of a store read at now. An error starts (or
// restarts) the hold; the first success after it starts the settle period.
func (s *storeSettle) observe(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.held, s.since, s.waiting = true, time.Time{}, false
		return
	}
	if s.held && s.since.IsZero() {
		s.since = now
	}
}

// hold records an outage the tracker did not observe itself. The settle
// period starts at the next successful read.
func (s *storeSettle) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held, s.since, s.waiting = true, time.Time{}, false
}

// settled reports whether verdicts may be given at now: no outage is being
// held, or store reads have succeeded continuously for at least window since
// it. firstWait is true the first time a caller is held back in the current
// settle period, so it can log the wait once.
func (s *storeSettle) settled(now time.Time, window time.Duration) (ok, firstWait bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held {
		return true, false
	}
	if !s.since.IsZero() && now.Sub(s.since) >= window {
		s.held, s.waiting = false, false
		return true, false
	}
	firstWait = !s.waiting
	s.waiting = true
	return false, firstWait
}
