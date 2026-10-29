package fcvm

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A terminal park pauses the guest, captures it and kills it. A forward that
// reaches the guest in between gets no response, and the gateway cannot tell
// it apart from a request the app failed (ADR-20261024090000). Park therefore
// holds new forwards on a gate for its whole run: once the VM is gone they find
// no instance, which the gateway re-routes to a fresh wake (ADR-1133). Forwards
// that reached the guest before the gate get a bounded drain before the pause.

// parkForwardDrainTimeout is api.ParkForwardDrainTimeoutSeconds; tests shorten it.
var parkForwardDrainTimeout = time.Duration(api.ParkForwardDrainTimeoutSeconds) * time.Second

const parkForwardDrainPoll = 20 * time.Millisecond

// holdForwardsForPark publishes the gate AwaitFrozen makes forwards wait on.
// The caller releases it after teardown, so a held forward sees the outcome.
func (m *Manager) holdForwardsForPark(inst *Instance) (release func()) {
	gate := make(chan struct{})
	m.mu.Lock()
	inst.parking = gate
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if inst.parking == gate {
			inst.parking = nil
		}
		m.mu.Unlock()
		close(gate)
	}
}

// drainForwardsForPark waits up to parkForwardDrainTimeout for the forwards
// that reached the guest before the park gate to finish. It returns how many
// were still running when it stopped waiting.
func (m *Manager) drainForwardsForPark(ctx context.Context, inst *Instance) int64 {
	if m.freezeActivity == nil {
		return 0
	}
	deadline := time.NewTimer(parkForwardDrainTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(parkForwardDrainPoll)
	defer tick.Stop()
	for {
		remaining := m.forwardsReachingGuest(inst)
		if remaining <= 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			return remaining
		case <-deadline.C:
			return remaining
		case <-tick.C:
		}
	}
}

// forwardsReachingGuest is the activity tracker's in-flight count minus the
// forwards held on the park gate. Reading the held count first keeps the
// result from undercounting: a held forward cannot leave before the gate
// closes, so every forward counted as held is still in the later total.
func (m *Manager) forwardsReachingGuest(inst *Instance) int64 {
	m.mu.Lock()
	held := inst.parkHeld
	m.mu.Unlock()
	inflight, _, _ := m.freezeActivity.Quiet(inst.Lease.Instance)
	return inflight - held
}
