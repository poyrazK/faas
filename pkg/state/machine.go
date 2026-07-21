package state

// Instance state machine (spec §6.1). schedd is the ONLY writer to the instances
// table and the sole owner of these transitions (spec §Component ownership). This
// file is the single definition of the states, the legal transitions between
// them, and which states count toward the two RAM/concurrency invariants (§6.2).
// The DB `instances.state` CHECK constraint mirrors these values.

// State is an instance lifecycle state. Values match the SQL CHECK set exactly.
type State string

const (
	// StateParked: on disk as a snapshot, zero resident RAM (§6.2-4).
	StateParked State = "parked"
	// StateWaking: restoring from a snapshot.
	StateWaking State = "waking"
	// StateColdBooting: booting from rootfs (restore missing/failed/stale).
	StateColdBooting State = "cold_booting"
	// StateRunning: serving on :8080.
	StateRunning State = "running"
	// StateSnapshotting: pausing + writing a snapshot before parking.
	StateSnapshotting State = "snapshotting"
	// StateStopped: cold with no usable snapshot; next wake is a cold boot.
	StateStopped State = "stopped"
	// StateFailed: crash-looped (≥3) or boot timed out; parked + operator notified.
	StateFailed State = "failed"
	// StateEvictingAccountDeleting: terminal state. schedd's deletion
	// subscriber (ADR-026) drops a live instance into this state when
	// the owning account scheduled deletion; the natural reaper
	// collects the microVM on its next pass. NOT in the legal
	// transitions map below on purpose — the subscriber is the only
	// writer, and the state is not reversible from the state machine.
	StateEvictingAccountDeleting State = "evicting_account_deleting"
)

// States lists every state (deterministic order for tests + CHECK generation).
var States = []State{
	StateParked, StateWaking, StateColdBooting, StateRunning,
	StateSnapshotting, StateStopped, StateFailed,
	StateEvictingAccountDeleting,
}

// transitions is the legal edge set of the state machine (spec §6.1).
var transitions = map[State][]State{
	// PARKED can wake (snapshot restore) or cold-boot (no snapshot, e.g. FC
	// upgrade → stale snap, or first deploy). The cold-boot branch is
	// spec §4.4's lazy re-snapshot path.
	StateParked:       {StateWaking, StateColdBooting},
	StateWaking:       {StateRunning, StateColdBooting, StateFailed, StateStopped, StateEvictingAccountDeleting},
	StateColdBooting:  {StateRunning, StateFailed, StateStopped, StateEvictingAccountDeleting},
	StateRunning:      {StateSnapshotting, StateStopped, StateFailed, StateEvictingAccountDeleting},
	StateSnapshotting: {StateParked, StateStopped, StateEvictingAccountDeleting},
	StateStopped:      {StateColdBooting},
	StateFailed:       {StateParked, StateColdBooting, StateStopped}, // manual recovery / lazy cold-boot
	// StateEvictingAccountDeleting is terminal — only the reaper
	// physically removes the VM; the row is then dropped by the
	// DeleteAccount walk after the 30-day grace window lapses.
	StateEvictingAccountDeleting: {},
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// CanTransition reports whether from→to is a legal edge. schedd must consult this
// before every write so an illegal transition can never reach the table.
func CanTransition(from, to State) bool {
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// CountsForConcurrency reports whether s counts toward an app's max_concurrency
// (invariant §6.2-1: ≤ max_concurrency in {WAKING, COLD_BOOTING, RUNNING}).
func (s State) CountsForConcurrency() bool {
	switch s {
	case StateWaking, StateColdBooting, StateRunning:
		return true
	default:
		return false
	}
}

// CountsForRAM reports whether s holds resident RAM and so counts against the
// admission ceiling (invariant §6.2-2: Σ(ram+8) over {WAKING, COLD_BOOTING,
// RUNNING, SNAPSHOTTING} ≤ 47,600 MB).
func (s State) CountsForRAM() bool {
	return s.CountsForConcurrency() || s == StateSnapshotting
}

// IsLive reports whether the named state is a live row that the
// scheduler should consider for work / eviction / RAM accounting.
// Equivalent to CountsForRAM (snapshot count is included because
// the snapshot middleware holds the VM paused but still resident).
//
// This is the single source of truth for "live" — schedd's
// eviction subscriber, any future quota eviction, and the
// MemStore-backed test helpers all read through this predicate so
// that adding a future state to the live set is a one-line change.
func IsLive(s string) bool { return State(s).CountsForRAM() }
