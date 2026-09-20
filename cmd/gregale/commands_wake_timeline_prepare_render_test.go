package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestRenderRestoreBreakdown_PrepareAndPreBootPhases pins the ADR-192
// fields: the pre-restore Manager phases render before the vmmd phases,
// and the loop-mount session is shown apart from stage_snapshot.
func TestRenderRestoreBreakdown_PrepareAndPreBootPhases(t *testing.T) {
	ev := api.WakeTimelineEvent{
		Kind: "wake.restore_breakdown",
		Data: map[string]any{
			"total_ms":                float64(1386),
			"setup_network_ms":        float64(5200),
			"lease_acquire_ms":        float64(2),
			"stage_pre_boot_files_ms": float64(240),
			"stage_snapshot_ms":       float64(6),
		},
	}
	got := renderRestoreBreakdown(ev)
	for _, want := range []string{
		"restore total=1386ms",
		"lease_acquire=2ms",
		"setup_network=5200ms",
		"stage_pre_boot_files=240ms",
		"stage_snapshot=6ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderRestoreBreakdown missing %q in %q", want, got)
		}
	}
	if strings.Index(got, "setup_network=") > strings.Index(got, "stage_snapshot=") {
		t.Errorf("prepare phases must render before vmmd phases: %q", got)
	}
}
