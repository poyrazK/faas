package state

import "fmt"

// DeadLetterSourceEventRoute is an event recipient whose routing failed and
// whose receipt was pruned (ADR-20261026163443). No invocation exists for it
// and its receipt is gone, so the entry keeps the published envelope and the
// failure for inspection but cannot be redriven.
const DeadLetterSourceEventRoute = "event_route"

// ErrDeadLetterNotReplayable refuses replay of an entry that has nothing left
// to redrive.
var ErrDeadLetterNotReplayable = fmt.Errorf("%w: dead-letter event cannot be replayed", ErrConflict)

// DeadLetterReplayable reports whether replay can redrive an entry from source.
// Replay-all skips the others, so they never hold a page of open entries.
func DeadLetterReplayable(source string) bool {
	return source != DeadLetterSourceEventRoute
}

// SeedDeadLetterEventForTest stores one ledger entry as written, for tests
// of sources whose capture the in-memory store does not model.
func (m *MemStore) SeedDeadLetterEventForTest(ev DeadLetterEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deadLetterSnapshots == nil {
		m.deadLetterSnapshots = make(map[string]DeadLetterEvent)
	}
	m.deadLetterSnapshots[ev.ID] = ev
}
