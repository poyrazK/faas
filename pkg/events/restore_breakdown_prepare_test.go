package events

import (
	"testing"
	"time"
)

// TestRestoreBreakdown_PrepareAndPreBootKeys pins the ADR-192 additions to
// the wake.restore_breakdown contract: the Manager.Wake phases that precede
// the vmmd window, and the loop-mount session split out of stage_snapshot.
func TestRestoreBreakdown_PrepareAndPreBootKeys(t *testing.T) {
	ev := RestoreBreakdown{
		EmitAt: time.Unix(0, 0).UTC(), WakeID: "w-prep", AppID: "a-prep", InstanceID: "i-prep",
		LeaseAcquireMs: 1, EnvPrepareMs: 2, PreNetworkMs: 3, SetupNetworkMs: 5200,
		StagePreBootFilesMs: 80, StageSnapshotMs: 2, TotalMs: 300,
	}
	p := ev.Payload()
	for key, want := range map[string]any{
		"lease_acquire_ms":        int64(1),
		"env_prepare_ms":          int64(2),
		"pre_network_ms":          int64(3),
		"setup_network_ms":        int64(5200),
		"stage_pre_boot_files_ms": int64(80),
		"stage_snapshot_ms":       int64(2),
		"total_ms":                int64(300),
	} {
		if got := p[key]; got != want {
			t.Errorf("payload[%q] = %v, want %v", key, got, want)
		}
	}
}
