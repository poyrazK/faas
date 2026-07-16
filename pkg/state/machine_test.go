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
		{StateRunning, StateParked},      // must snapshot first
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
	// Invariant §6.2-2: these four hold resident RAM.
	want := map[State]bool{StateWaking: true, StateColdBooting: true, StateRunning: true, StateSnapshotting: true}
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
