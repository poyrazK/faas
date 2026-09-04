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
	// reconciler drops a live instance into this state when the owning
	// account schedules deletion, destroys the VM, and leaves the row
	// until the account's hard-delete grace sweep removes it.
	StateEvictingAccountDeleting State = "evicting_account_deleting"
	// StateMigrating (Tier A5 / ADR-066) — transient state stamped
	// at Phase 3 of the four-phase cross-node live-instance handoff.
	// The dying vmmd has paused the VM and written the snapshot;
	// the new owner vmmd is restoring. CountsForRAM returns true
	// (the snapshot occupies resident RAM on the dying node until
	// the lease resolves). NOT a wake-side state: only schedd's
	// Engine.MigrateLiveInstances drives a transition into here, and
	// the only legal exit is StateRunning (commit succeeded) or
	// StateParked (commit failed; the dying vmmd resumes the VM
	// and the snapshot stays where it was). Mirrors the
	// StateEvictingAccountDeleting precedent — a transient state
	// outside the wake/reap hot path.
	StateMigrating State = "migrating"
)

// States lists every state (deterministic order for tests + CHECK generation).
var States = []State{
	StateParked, StateWaking, StateColdBooting, StateRunning,
	StateSnapshotting, StateStopped, StateFailed,
	StateEvictingAccountDeleting, StateMigrating,
}

// transitions is the legal edge set of the state machine (spec §6.1).
//
// Workstream B (issue #1184 / ADR-137) adds the RUNNING → PARKED,
// COLD_BOOTING → PARKED, WAKING → PARKED edges for the
// recovery_recreate path. The recovery arbiter decides an instance
// has no usable snapshot to migrate (the source VM died with its
// host) and needs a way to land the row in PARKED without the
// SNAPSHOTTING detour — there is nothing to snapshot. The audit
// row's kind='recovery_recreate' tag is the discriminator that
// distinguishes a recovery_recreate landing from a normal
// idle-timeout Park. Normal Parks still go RUNNING → SNAPSHOTTING
// → PARKED (the snapshot-then-stop flow); the new direct edges
// are reserved for the recreate primitive only.
var transitions = map[State][]State{
	// PARKED can wake (snapshot restore) or cold-boot (no snapshot, e.g. FC
	// upgrade → stale snap, or first deploy). The cold-boot branch is
	// spec §4.4's lazy re-snapshot path.
	StateParked:       {StateWaking, StateColdBooting},
	StateWaking:       {StateRunning, StateColdBooting, StateFailed, StateStopped, StateEvictingAccountDeleting, StateParked},
	StateColdBooting:  {StateRunning, StateFailed, StateStopped, StateEvictingAccountDeleting, StateParked},
	StateRunning:      {StateSnapshotting, StateStopped, StateFailed, StateEvictingAccountDeleting, StateMigrating, StateParked},
	StateSnapshotting: {StateParked, StateStopped, StateEvictingAccountDeleting},
	StateStopped:      {StateColdBooting},
	StateFailed:       {StateParked, StateColdBooting, StateStopped}, // manual recovery / lazy cold-boot
	// StateEvictingAccountDeleting is terminal — the lifecycle
	// reconciler physically removes the VM; the row is then dropped
	// by the DeleteAccount walk after the 30-day grace window lapses.
	StateEvictingAccountDeleting: {},
	// StateMigrating → RUNNING on commit; → PARKED on rollback.
	// STOPPED and EVICTING_ACCOUNT_DELETING are exceptional cleanup
	// exits used when deletion wins a migration race.
	// (the dying vmmd resumes the VM and the snapshot stays).
	// Either edge ends the transient; the row is no longer
	// considered migrating from that moment. Engine.MigrateLiveInstances
	// owns both transitions.
	StateMigrating: {StateRunning, StateParked, StateFailed, StateStopped, StateEvictingAccountDeleting},
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
// RUNNING, SNAPSHOTTING, MIGRATING} ≤ 47,600 MB).
//
// StateMigrating holds the paused-VM snapshot resident on the dying
// node during the four-phase handoff (ADR-066). It counts as live
// RAM until either the commit succeeds (edge → RUNNING on the new
// owner) or the rollback fires (edge → PARKED; the snapshot is
// freed on the dying node once the dying vmmd resumes).
func (s State) CountsForRAM() bool {
	return s.CountsForConcurrency() || s == StateSnapshotting || s == StateMigrating
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

// IsMeteredSkippableMode reports whether an instance in `mode` should
// be skipped by the meter sampler (issue #72 / ADR-125 + ADR-137).
//
// Pre-M-2: the sampler had an inline `mode == string(state.InstanceModeMirror)`
// check at pkg/meter/sampler.go:373. M-2 commit 4 introduces this
// helper as the single source of truth for the meter-skip predicate;
// commit 9 swaps the inline check for `state.IsMeteredSkippableMode`.
//
// The mirror-mode skip is preserved verbatim (ADR-125). The new
// M-2 modes (worker, service, job) are NOT skipped — they bill at
// the standard mb_seconds rate (spec §4.7 formula unchanged).
func IsMeteredSkippableMode(mode string) bool {
	return mode == string(InstanceModeMirror)
}

// CountsForRAMByMode reports whether an instance in `mode` counts
// against the platform's RAM admission ceiling when the state
// predicate alone would have said "yes" (CountsForRAM). Today every
// mode counts equally; the helper exists so a future mode (e.g.
// `ephemeral` for build VMs that share a tenant-slice cgroup) can
// opt out without rewriting every caller. The mirror mode is already
// excluded by IsMeteredSkippableMode at the meter layer; this
// helper is the parallel hook for the schedd admission path if a
// similar carve-out ever lands there.
//
// Return value semantics (ADR-137 §Decision 1):
//
//   - normal / worker / service / job → true (RAM counts)
//   - mirror → true at the RAM-admission layer; the meter sampler
//     skips it via IsMeteredSkippableMode (different layer). A
//     mirror VM still consumes the tenant RAM budget while it is
//     RUNNING; it just does not bill.
func CountsForRAMByMode(mode string) bool {
	switch InstanceMode(mode) {
	case InstanceModeNormal,
		InstanceModeWorker,
		InstanceModeService,
		InstanceModeJob,
		InstanceModeMirror:
		return true
	default:
		// Unknown mode defaults to counting — fail-closed at the
		// admission layer so an unexpected mode value cannot
		// silently consume more than the budget allows. The CHECK
		// constraint on the column (migrations/00532) is the
		// load-bearing defence; this is belt-and-braces.
		return true
	}
}
