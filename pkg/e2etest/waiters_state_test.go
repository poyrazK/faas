package e2etest

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestParkedHistoryDoesNotSatisfyAppParkWait(t *testing.T) {
	for _, resident := range []state.State{
		state.StateWaking, state.StateColdBooting, state.StateRunning,
		state.StateDraining, state.StateSnapshotting, state.StateMigrating, state.StateWarm,
	} {
		t.Run(string(resident), func(t *testing.T) {
			instances := []state.Instance{
				{ID: "snapshot-owner", State: string(state.StateParked)},
				{ID: "restored", State: string(resident)},
			}
			if instanceStateReached(instances, state.StateParked, true) {
				t.Fatal("historical snapshot row concealed a resident restored instance")
			}
			instances[1].State = string(state.StateParked)
			if !instanceStateReached(instances, state.StateParked, true) {
				t.Fatal("app did not settle after its restored instance parked")
			}
		})
	}
}

func TestAppParkWaitRequiresParkedSnapshot(t *testing.T) {
	for _, instances := range [][]state.Instance{
		nil,
		{{ID: "failed", State: string(state.StateFailed)}},
		{{ID: "stopped", State: string(state.StateStopped)}},
	} {
		if instanceStateReached(instances, state.StateParked, true) {
			t.Fatal("absence of resident instances alone satisfied the park wait")
		}
	}
}

func TestAnyInstanceWaitRetainsHistoricalStateSemantics(t *testing.T) {
	instances := []state.Instance{
		{ID: "snapshot-owner", State: string(state.StateParked)},
		{ID: "restored", State: string(state.StateRunning)},
	}
	if !instanceStateReached(instances, state.StateParked, false) {
		t.Fatal("ordinary instance wait lost historical state matching")
	}
	if !instanceStateReached(instances, state.StateRunning, false) {
		t.Fatal("ordinary instance wait lost running state matching")
	}
}
