package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestLegalTransitions(t *testing.T) {
	legal := [][2]state.State{
		{state.StateParked, state.StateWaking},
		{state.StateWaking, state.StateRunning},
		{state.StateWaking, state.StateColdBooting}, // restore failed → fallback
		{state.StateColdBooting, state.StateRunning},
		{state.StateColdBooting, state.StateWarm}, // paused restore retained in pool
		{state.StateWarm, state.StateRunning},     // request resumes a pooled VM
		{state.StateWarm, state.StateParked},      // pool scale-down / stale cleanup
		{state.StateRunning, state.StateSnapshotting},
		{state.StateRunning, state.StateDraining},
		{state.StateDraining, state.StateStopped},
		{state.StateDraining, state.StateFailed},
		{state.StateDraining, state.StateEvictingAccountDeleting},
		{state.StateSnapshotting, state.StateParked},
		{state.StateSnapshotting, state.StateStopped}, // snapshot failed
		{state.StateStopped, state.StateColdBooting},  // next wake cold boots
		{state.StateRunning, state.StateFailed},       // crash loop
		{state.StateColdBooting, state.StateFailed},   // boot timeout
		// Workstream B / issue #1184 / ADR-137: the recovery_recreate
		// primitive (Engine.RecreateInstance) lands a stranded live
		// row in PARKED without the SNAPSHOTTING detour. The audit
		// row's kind='recovery_recreate' tag discriminates this from
		// a normal idle-timeout Park. Normal Parks still go
		// RUNNING → SNAPSHOTTING → PARKED.
		{state.StateRunning, state.StateParked},
		{state.StateColdBooting, state.StateParked},
		{state.StateWaking, state.StateParked},
		// ADR-1151: an idle instance freezes in place and thaws for a
		// request; TTL, pressure and failed thaws park it.
		{state.StateRunning, state.StateFrozen},
		{state.StateFrozen, state.StateRunning},
		{state.StateFrozen, state.StateParked},
		{state.StateFrozen, state.StateStopped},
		{state.StateFrozen, state.StateEvictingAccountDeleting},
	}
	for _, e := range legal {
		if !state.CanTransition(e[0], e[1]) {
			t.Errorf("%s→%s should be legal", e[0], e[1])
		}
	}
}

func TestIllegalTransitions(t *testing.T) {
	illegal := [][2]state.State{
		{state.StateParked, state.StateRunning},      // must wake first
		{state.StateParked, state.StateParked},       // no self-loop
		{state.StateRunning, state.StateColdBooting}, // can't re-boot a running vm
		{state.StateFailed, state.StateRunning},      // failed re-parks, not resumes
		{state.StateStopped, state.StateWaking},      // stopped has no snapshot to restore
		{state.StateParked, state.StateFrozen},       // only a running VM can freeze
		{state.StateWarm, state.StateFrozen},         // warm VMs are already paused and billed
		{state.StateSnapshotting, state.StateFrozen}, // freeze needs an existing snapshot
		{state.StateFrozen, state.StateSnapshotting}, // a frozen VM is destroyed, not snapshotted
	}
	for _, e := range illegal {
		if state.CanTransition(e[0], e[1]) {
			t.Errorf("%s→%s should be illegal", e[0], e[1])
		}
	}
}

func TestEveryStateValidAndReachable(t *testing.T) {
	if len(state.States) != len((*state.Transitions)) {
		t.Fatalf("States list (%d) and transition table (%d) out of sync", len(state.States), len((*state.Transitions)))
	}
	// Every state must be a transition target of some other state (reachable),
	// except the entry state PARKED (reached via the deploy pipeline).
	reachable := map[state.State]bool{state.StateParked: true}
	for _, targets := range *state.Transitions {
		for _, to := range targets {
			reachable[to] = true
		}
	}
	for _, s := range state.States {
		if !s.Valid() {
			t.Errorf("state %s not valid", s)
		}
		if !reachable[s] {
			t.Errorf("state %s is unreachable", s)
		}
	}
}

func TestConcurrencyAccounting(t *testing.T) {
	// Draining remains concurrency-accounted until its resident VM is gone.
	want := map[state.State]bool{state.StateWaking: true, state.StateColdBooting: true, state.StateRunning: true, state.StateDraining: true}
	for _, s := range state.States {
		if got := s.CountsForConcurrency(); got != want[s] {
			t.Errorf("%s.CountsForConcurrency() = %v, want %v", s, got, want[s])
		}
	}
}

func TestRAMAccounting(t *testing.T) {
	// Invariant §6.2-2: these seven hold resident RAM. Tier A5
	// (ADR-066) added StateMigrating — the paused-VM
	// snapshot is resident on the dying node during the
	// four-phase handoff; the RAM must count against the
	// admission ceiling so the invariant stays honest.
	want := map[state.State]bool{
		state.StateWaking: true, state.StateColdBooting: true,
		state.StateRunning: true, state.StateSnapshotting: true,
		state.StateMigrating: true,
		state.StateWarm:      true, state.StateDraining: true,
		state.StateFrozen: true,
	}
	for _, s := range state.States {
		if got := s.CountsForRAM(); got != want[s] {
			t.Errorf("%s.CountsForRAM() = %v, want %v", s, got, want[s])
		}
	}
	// A parked instance must not count for RAM (§6.2-4).
	if state.StateParked.CountsForRAM() {
		t.Error("parked instances must hold zero resident RAM")
	}
}

// TestIsLive pins the IsLive predicate for every state in
// machine.go::States. IsLive is the single source of truth for
// "live row" semantics — schedd's eviction subscriber,
// ListAllInstances' filter (pgstore.go:1683), and any future
// quota eviction read through it. The test asserts both the
// string-typed entry point (machine.go:105, used by pgstore.go
// + schedd) and the State-typed CountsForRAM that IsLive
// delegates to, so a future refactor that drops the
// `State(s).CountsForRAM()` indirection surfaces here.
//
// The set's exact membership is the load-bearing contract:
// {WAKING, COLD_BOOTING, RUNNING, DRAINING, SNAPSHOTTING, MIGRATING, WARM}.
// PARKED,
// STOPPED, FAILED, EVICTING_ACCOUNT_DELETING are NOT live.
// Tier A5 (ADR-066) added MIGRATING to this set.
//
// The want map is keyed by State (not string), so renaming a
// state constant fails at compile time rather than silently
// changing the answer.
func TestIsLive(t *testing.T) {
	// State-typed want table: rename a constant → compile error,
	// not a silent miss.
	want := map[state.State]bool{
		state.StateWaking:                  true,
		state.StateColdBooting:             true,
		state.StateRunning:                 true,
		state.StateDraining:                true,
		state.StateSnapshotting:            true,
		state.StateMigrating:               true,
		state.StateWarm:                    true,
		state.StateFrozen:                  true,
		state.StateParked:                  false,
		state.StateStopped:                 false,
		state.StateFailed:                  false,
		state.StateEvictingAccountDeleting: false,
	}
	for _, s := range state.States {
		t.Run(string(s), func(t *testing.T) {
			// String-typed entry point — the surface schedd
			// and pgstore actually call.
			got := state.IsLive(string(s))
			if got != want[s] {
				t.Errorf("IsLive(%q) = %v, want %v", string(s), got, want[s])
			}
			// State-typed entry point — pins the indirection
			// so a future refactor of IsLive that drops the
			// `State(s).CountsForRAM()` hop surfaces here.
			if gotState := s.CountsForRAM(); gotState != want[s] {
				t.Errorf("%s.CountsForRAM() = %v, want %v "+
					"(IsLive must delegate to CountsForRAM)",
					s, gotState, want[s])
			}
		})
	}
}

// TestBillable: §4.7 bills plan RAM + 8 MB per resident second, except a
// frozen instance, which is resident but unbilled (ADR-1151).
func TestBillable(t *testing.T) {
	for _, s := range state.States {
		want := s.CountsForRAM() && s != state.StateFrozen
		if got := s.Billable(); got != want {
			t.Errorf("%s.Billable() = %v, want %v", s, got, want)
		}
	}
	if state.StateFrozen.Billable() {
		t.Error("frozen instances must not be billed")
	}
	if !state.StateWarm.Billable() {
		t.Error("warm instances stay billed: they reserve full capacity")
	}
}
