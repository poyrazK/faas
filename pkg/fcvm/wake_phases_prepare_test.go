// adr: 192
// spec: §6.3
package fcvm

import (
	"testing"
	"time"
)

// TestWakePhases_PrepareTimings pins the ADR-192 projection from the slog
// phase marks to the typed pre-restore timings the breakdown event carries.
func TestWakePhases_PrepareTimings(t *testing.T) {
	w := &wakePhases{}
	base := time.Unix(1_700_000_000, 0)
	w.start, w.last = base, base
	w.phases = []wakePhase{
		{name: "lease_acquire", ms: 3},
		{name: "env_prepare", ms: 7},
		{name: "pre_network", ms: 11},
		{name: "setup_network", ms: 4200},
		{name: "bring_up", ms: 180}, // after Restore; must not leak into prepare
		{name: "setup_network", ms: 5},
	}
	got := w.prepareTimings()
	want := WakePrepareTimings{LeaseAcquireMs: 3, EnvPrepareMs: 7, PreNetworkMs: 11, SetupNetworkMs: 4205}
	if got != want {
		t.Fatalf("prepareTimings = %+v, want %+v", got, want)
	}
}

func TestWakePhases_PrepareTimings_NilSafe(t *testing.T) {
	var w *wakePhases
	if got := w.prepareTimings(); got != (WakePrepareTimings{}) {
		t.Fatalf("nil prepareTimings = %+v, want zero", got)
	}
}

func TestRestoreBreakdown_SplitsPreBootFilesFromSnapshotStage(t *testing.T) {
	// The struct carries both windows separately so the event can show the
	// loop-mount session next to the two-syscall mem/vmstate bind.
	b := restoreTimingBreakdown{StagePreBootFilesMs: 80, StageSnapshotMs: 2,
		Prepare: WakePrepareTimings{SetupNetworkMs: 5200}}
	if b.StagePreBootFilesMs+b.StageSnapshotMs != 82 || b.Prepare.SetupNetworkMs != 5200 {
		t.Fatalf("unexpected breakdown shape: %+v", b)
	}
}
