package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"slices"
	"testing"
)

// LiveStates feeds SQL filters, so it must name exactly the states IsLive
// accepts. Every value of the instances.state CHECK is listed here.
func TestLiveStatesMatchIsLive(t *testing.T) {
	all := []state.State{state.StateParked, state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateSnapshotting, state.StateStopped,
		state.StateFailed, state.StateEvictingAccountDeleting, state.StateMigrating, state.StateWarm, state.StateDraining, state.StateFrozen, "pending"}
	live := state.LiveStates()
	for _, s := range all {
		if got := slices.Contains(live, string(s)); got != state.IsLive(string(s)) {
			t.Errorf("state %q: in LiveStates = %v, IsLive = %v", s, got, state.IsLive(string(s)))
		}
	}
	for _, s := range live {
		if !slices.Contains(all, state.State(s)) {
			t.Errorf("LiveStates names %q, which is not an instances.state value", s)
		}
	}
}
