package state

import "testing"

func TestLegalTransitions(t *testing.T) {
	legal := [][2]State{
		{StateParked, StateWaking},
		{StateWaking, StateRunning},
		{StateWaking, StateColdBooting}, // restore failed → fallback
		{StateColdBooting, StateRunning},
		{StateRunning, StateSnapshotting},
		{StateSnapshotting, StateParked},
		{StateSnapshotting, StateStopped}, // snapshot failed
		{StateStopped, StateColdBooting},  // next wake cold boots
		{StateRunning, StateFailed},       // crash loop
		{StateColdBooting, StateFailed},   // boot timeout
		// Workstream B / issue #1184 / ADR-137: the recovery_recreate
		// primitive (Engine.RecreateInstance) lands a stranded live
		// row in PARKED without the SNAPSHOTTING detour. The audit
		// row's kind='recovery_recreate' tag discriminates this from
		// a normal idle-timeout Park. Normal Parks still go
		// RUNNING → SNAPSHOTTING → PARKED.
		{StateRunning, StateParked},
		{StateColdBooting, StateParked},
		{StateWaking, StateParked},
	}
	for _, e := range legal {
		if !CanTransition(e[0], e[1]) {
			t.Errorf("%s→%s should be legal", e[0], e[1])
		}
	}
}

func TestIllegalTransitions(t *testing.T) {
	illegal := [][2]State{
		{StateParked, StateRunning},      // must wake first
		{StateParked, StateParked},       // no self-loop
		{StateRunning, StateColdBooting}, // can't re-boot a running vm
		{StateFailed, StateRunning},      // failed re-parks, not resumes
		{StateStopped, StateWaking},      // stopped has no snapshot to restore
	}
	for _, e := range illegal {
		if CanTransition(e[0], e[1]) {
			t.Errorf("%s→%s should be illegal", e[0], e[1])
		}
	}
}

func TestEveryStateValidAndReachable(t *testing.T) {
	if len(States) != len(transitions) {
		t.Fatalf("States list (%d) and transition table (%d) out of sync", len(States), len(transitions))
	}
	// Every state must be a transition target of some other state (reachable),
	// except the entry state PARKED (reached via the deploy pipeline).
	reachable := map[State]bool{StateParked: true}
	for _, targets := range transitions {
		for _, to := range targets {
			reachable[to] = true
		}
	}
	for _, s := range States {
		if !s.Valid() {
			t.Errorf("state %s not valid", s)
		}
		if !reachable[s] {
			t.Errorf("state %s is unreachable", s)
		}
	}
}

func TestConcurrencyAccounting(t *testing.T) {
	// Invariant §6.2-1: only these three count toward max_concurrency.
	want := map[State]bool{StateWaking: true, StateColdBooting: true, StateRunning: true}
	for _, s := range States {
		if got := s.CountsForConcurrency(); got != want[s] {
			t.Errorf("%s.CountsForConcurrency() = %v, want %v", s, got, want[s])
		}
	}
}

func TestRAMAccounting(t *testing.T) {
	// Invariant §6.2-2: these five hold resident RAM. Tier A5
	// (ADR-066) added StateMigrating — the paused-VM
	// snapshot is resident on the dying node during the
	// four-phase handoff; the RAM must count against the
	// admission ceiling so the invariant stays honest.
	want := map[State]bool{
		StateWaking: true, StateColdBooting: true,
		StateRunning: true, StateSnapshotting: true,
		StateMigrating: true,
	}
	for _, s := range States {
		if got := s.CountsForRAM(); got != want[s] {
			t.Errorf("%s.CountsForRAM() = %v, want %v", s, got, want[s])
		}
	}
	// A parked instance must not count for RAM (§6.2-4).
	if StateParked.CountsForRAM() {
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
// {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING, MIGRATING} —
// the same five states counted for RAM (§6.2-2). PARKED,
// STOPPED, FAILED, EVICTING_ACCOUNT_DELETING are NOT live.
// Tier A5 (ADR-066) added MIGRATING to this set.
//
// The want map is keyed by State (not string), so renaming a
// state constant fails at compile time rather than silently
// changing the answer.
func TestIsLive(t *testing.T) {
	// State-typed want table: rename a constant → compile error,
	// not a silent miss.
	want := map[State]bool{
		StateWaking:                  true,
		StateColdBooting:             true,
		StateRunning:                 true,
		StateSnapshotting:            true,
		StateMigrating:               true,
		StateParked:                  false,
		StateStopped:                 false,
		StateFailed:                  false,
		StateEvictingAccountDeleting: false,
	}
	for _, s := range States {
		t.Run(string(s), func(t *testing.T) {
			// String-typed entry point — the surface schedd
			// and pgstore actually call.
			got := IsLive(string(s))
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
