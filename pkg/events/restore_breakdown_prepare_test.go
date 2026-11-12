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

// TestBreakdowns_BackingCheckKeysAreOptional pins the ADR-510 backing check
// keys on both breakdowns: present when the check took measurable time,
// absent otherwise so earlier readers see an unchanged payload.
func TestBreakdowns_BackingCheckKeysAreOptional(t *testing.T) {
	keys := []string{"backing_check_ms", "backing_sidecar_ms", "backing_fetch_ms", "backing_hash_ms"}
	for _, tc := range []struct {
		name    string
		payload map[string]any
		want    []int64 // nil: keys absent
	}{
		{name: "restore without a check", payload: RestoreBreakdown{TotalMs: 300}.Payload()},
		{
			name: "restore after a check",
			payload: RestoreBreakdown{
				BackingCheckMs: 2530, BackingSidecarMs: 4, BackingFetchMs: 1, BackingHashMs: 2524, TotalMs: 664,
			}.Payload(),
			want: []int64{2530, 4, 1, 2524},
		},
		{name: "cold boot without a check", payload: ColdBootBreakdown{TotalMs: 1260}.Payload()},
		{
			name: "cold boot after a refusal",
			payload: ColdBootBreakdown{
				BackingCheckMs: 443, BackingSidecarMs: 1, BackingHashMs: 442, TotalMs: 1260,
			}.Payload(),
			want: []int64{443, 1, 0, 442},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, key := range keys {
				got, present := tc.payload[key]
				if tc.want == nil {
					if present {
						t.Errorf("payload[%q] = %v, want absent", key, got)
					}
					continue
				}
				if got != tc.want[i] {
					t.Errorf("payload[%q] = %v, want %d", key, got, tc.want[i])
				}
			}
		})
	}
}
