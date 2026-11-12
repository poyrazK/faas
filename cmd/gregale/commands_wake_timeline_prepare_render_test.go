package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
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
	got := main.RenderRestoreBreakdown(ev)
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

// TestRenderBreakdowns_BackingCheck pins the ADR-510 backing check keys on
// both breakdowns: rendered when present, after the network and before the
// vmmd phases.
func TestRenderBreakdowns_BackingCheck(t *testing.T) {
	backing := map[string]any{
		"backing_check_ms": float64(2530), "backing_sidecar_ms": float64(4),
		"backing_fetch_ms": float64(1), "backing_hash_ms": float64(2524),
	}
	for _, tc := range []struct {
		name   string
		kind   string
		render func(api.WakeTimelineEvent) string
		before string
	}{
		{name: "restore", kind: "wake.restore_breakdown", render: main.RenderRestoreBreakdown, before: "chroot="},
		{name: "cold boot", kind: "wake.cold_boot_breakdown", render: main.RenderColdBootBreakdown, before: "chroot="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{"total_ms": float64(664), "chroot_ms": float64(16)}
			for k, v := range backing {
				data[k] = v
			}
			got := tc.render(api.WakeTimelineEvent{Kind: tc.kind, Data: data})
			for _, want := range []string{"backing_check=2530ms", "backing_sidecar=4ms", "backing_fetch=1ms", "backing_hash=2524ms"} {
				if !strings.Contains(got, want) {
					t.Errorf("render missing %q in %q", want, got)
				}
			}
			if strings.Index(got, "backing_check=") > strings.Index(got, tc.before) {
				t.Errorf("backing check must render before %s: %q", tc.before, got)
			}
			without := tc.render(api.WakeTimelineEvent{Kind: tc.kind, Data: map[string]any{"total_ms": float64(664)}})
			if strings.Contains(without, "backing_") {
				t.Errorf("render without a check shows one: %q", without)
			}
		})
	}
}
