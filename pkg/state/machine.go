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
)

// States lists every state (deterministic order for tests + CHECK generation).
var States = []State{
	StateParked, StateWaking, StateColdBooting, StateRunning,
	StateSnapshotting, StateStopped, StateFailed,
}

// transitions is the legal edge set of the state machine (spec §6.1).
var transitions = map[State][]State{
	// PARKED can wake (snapshot restore) or cold-boot (no snapshot, e.g. FC
	// upgrade → stale snap, or first deploy). The cold-boot branch is
	// spec §4.4's lazy re-snapshot path.
	StateParked:       {StateWaking, StateColdBooting},
	StateWaking:       {StateRunning, StateColdBooting, StateFailed, StateStopped},
	StateColdBooting:  {StateRunning, StateFailed, StateStopped},
	StateRunning:      {StateSnapshotting, StateStopped, StateFailed},
	StateSnapshotting: {StateParked, StateStopped},
	StateStopped:      {StateColdBooting},
	StateFailed:       {StateParked, StateColdBooting, StateStopped}, // manual recovery / lazy cold-boot
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
