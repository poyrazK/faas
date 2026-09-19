package state

import (
	"testing"
	"time"
)

// TestKeysWarmSnapMemoryKey pins the warm-tier snapshot key helpers
// introduced by issue #470 / PR #470-FU-A. Both functions are pure
// string formatters; the test asserts the documented shape so a
// accidental rename doesn't silently break the GC wildcard predicate.
func TestCoverageSlice13KeysWarmSnapMemoryKey(t *testing.T) {
	depID := "abc-123"
	if got, want := WarmSnapMemKey(depID), "snap/"+depID+"/warm/mem"; got != want {
		t.Errorf("WarmSnapMemKey = %q, want %q", got, want)
	}
	if got, want := WarmSnapVMStateKey(depID), "snap/"+depID+"/warm/vmstate"; got != want {
		t.Errorf("WarmSnapVMStateKey = %q, want %q", got, want)
	}
	// Pin the cold-tier sibling for the documented contrast.
	if got, want := SnapMemKey(depID), "snap/"+depID+"/mem"; got != want {
		t.Errorf("SnapMemKey = %q, want %q", got, want)
	}
	if got, want := SnapVMStateKey(depID), "snap/"+depID+"/vmstate"; got != want {
		t.Errorf("SnapVMStateKey = %q, want %q", got, want)
	}
}

// TestCoverageSlice13DeploymentSidecarRAMs drives the zero-sidecar
// branch of DeploymentSidecarRAMs (deployment_sidecar_rams.go:67).
// The positive path is exercised by every sweeper that adds a sidecar;
// the MemStore fixture here pins the empty-list output.
func TestCoverageSlice13DeploymentSidecarRAMs(t *testing.T) {
	m, ctx, _, _, dep := memCoverageFixture(t)
	if got, err := m.DeploymentSidecarRAMs(ctx, dep.ID); err != nil || len(got) != 0 {
		t.Fatalf("DeploymentSidecarRAMs empty = %v, %v", got, err)
	}
}

// TestMemStoreHeartbeatGapClassification pins the heartbeat-gap
// classifier that schedd uses to decide whether a missed liveness
// ping is recoverable. The function is a pure switch between
// duration-based branches; the test asserts the boundary points
// documented in heartbeat_gap.go.
func TestCoverageSlice13MemStoreHeartbeatGapClassification(t *testing.T) {
	now := time.Now()
	interval := time.Second
	staleness := DefaultHeartbeatStaleness

	// Zero prev → no baseline → empty summary.
	if got := ClassifyHeartbeatGap(time.Time{}, now, interval, staleness); got.Missed || got.Stale {
		t.Errorf("zero prev: got %+v, want empty", got)
	}
	// Healthy tick → empty summary.
	if got := ClassifyHeartbeatGap(now, now.Add(interval/2), interval, staleness); got.Missed || got.Stale {
		t.Errorf("healthy tick: got %+v, want empty", got)
	}
	// Missed inside staleness window → Missed=true, Stale=false.
	if got := ClassifyHeartbeatGap(now, now.Add(staleness/2), interval, staleness); !got.Missed || got.Stale {
		t.Errorf("missed inside staleness: got %+v, want Missed=true", got)
	}
	// Stale past window → Missed=true, Stale=true.
	if got := ClassifyHeartbeatGap(now, now.Add(2*staleness), interval, staleness); !got.Missed || !got.Stale {
		t.Errorf("stale past window: got %+v, want Missed=true Stale=true", got)
	}
}

// TestMemStoreInstanceStateMachine covers the Valid/CanTransition
// surfaces of the instance-state machine. Both are pure
// functions over the State enum.
func TestCoverageSlice13MemStoreInstanceStateMachine(t *testing.T) {
	// All documented states must be valid.
	for _, s := range []State{
		StateParked, StateWaking, StateColdBooting, StateRunning,
		StateSnapshotting, StateStopped, StateFailed,
		StateEvictingAccountDeleting, StateMigrating,
		StateWarm,
	} {
		if !s.Valid() {
			t.Errorf("State(%q).Valid() = false", s)
		}
	}
	// An empty string is not a valid state.
	if State("").Valid() {
		t.Error("empty state valid = true, want false")
	}

	// CountsForConcurrency: live states only.
	for _, s := range []State{StateWaking, StateColdBooting, StateRunning} {
		if !s.CountsForConcurrency() {
			t.Errorf("State(%q).CountsForConcurrency() = false, want true", s)
		}
	}
	if State("").CountsForConcurrency() {
		t.Error("empty state counts for concurrency = true, want false")
	}

	// CountsForRAM is broader (includes snapshotting + stopped).
	for _, s := range []State{
		StateWaking, StateColdBooting, StateRunning, StateSnapshotting,
		StateMigrating,
		StateWarm,
	} {
		if !s.CountsForRAM() {
			t.Errorf("State(%q).CountsForRAM() = false, want true", s)
		}
	}
	if StateParked.CountsForRAM() {
		t.Error("StateParked.CountsForRAM() = true, want false")
	}

	// CanTransition: the canonical happy path.
	if !CanTransition(StateParked, StateWaking) {
		t.Error("Parked → Waking disallowed")
	}
	if !CanTransition(StateRunning, StateStopped) {
		t.Error("Running → Stopped disallowed")
	}
}
