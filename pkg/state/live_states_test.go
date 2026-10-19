package state

import (
	"slices"
	"testing"
)

// LiveStates feeds SQL filters, so it must name exactly the states IsLive
// accepts. Every value of the instances.state CHECK is listed here.
func TestLiveStatesMatchIsLive(t *testing.T) {
	all := []State{StateParked, StateWaking, StateColdBooting, StateRunning, StateSnapshotting, StateStopped,
		StateFailed, StateEvictingAccountDeleting, StateMigrating, StateWarm, StateDraining, StateFrozen, "pending"}
	live := LiveStates()
	for _, s := range all {
		if got := slices.Contains(live, string(s)); got != IsLive(string(s)) {
			t.Errorf("state %q: in LiveStates = %v, IsLive = %v", s, got, IsLive(string(s)))
		}
	}
	for _, s := range live {
		if !slices.Contains(all, State(s)) {
			t.Errorf("LiveStates names %q, which is not an instances.state value", s)
		}
	}
}
