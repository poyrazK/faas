package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestApplyRolloutFlag pins ADR-1450's mapping of --rollout onto the flags
// deploy already understands, and its refusal to mix with them.
func TestApplyRolloutFlag(t *testing.T) {
	for _, tc := range []struct {
		rollout    string
		wantSafe   bool
		wantPreset string
		wantFlag   string
	}{
		{rollout: "safe", wantSafe: true, wantFlag: "safe"},
		{rollout: "fast", wantPreset: api.FastReleaseCanaryPreset, wantFlag: "canary-preset"},
		{rollout: "immediate", wantPreset: "none", wantFlag: "canary-preset"},
	} {
		explicit := map[string]bool{"rollout": true}
		safe, preset := false, ""
		if err := applyRolloutFlag(tc.rollout, explicit, &safe, &preset); err != nil {
			t.Fatalf("--rollout %s: %v", tc.rollout, err)
		}
		if safe != tc.wantSafe || preset != tc.wantPreset || !explicit[tc.wantFlag] {
			t.Fatalf("--rollout %s = safe:%v preset:%q explicit:%v", tc.rollout, safe, preset, explicit)
		}
	}

	safe, preset := false, ""
	if err := applyRolloutFlag("slow", map[string]bool{}, &safe, &preset); err == nil || !strings.Contains(err.Error(), "safe, fast or immediate") {
		t.Fatalf("--rollout slow = %v, want a closed-set error", err)
	}
	for _, other := range rolloutFlagConflicts {
		err := applyRolloutFlag("fast", map[string]bool{"rollout": true, other: true}, &safe, &preset)
		if err == nil || !strings.Contains(err.Error(), "--"+other) {
			t.Fatalf("--rollout with --%s = %v, want a conflict naming it", other, err)
		}
	}
	if err := applyRolloutFlag("fast", map[string]bool{"rollout": true, "no-traffic": true}, &safe, &preset); err != nil {
		t.Fatalf("--rollout with --no-traffic is left to deploy's own check, got %v", err)
	}
}

// TestFollowRolloutOnlyForAPersonAtATerminal: JSON and non-interactive runs
// never follow (ADR-1450).
func TestFollowRolloutOnlyForAPersonAtATerminal(t *testing.T) {
	oldJSON, oldNonInteractive := jsonOutput, nonInteractive
	t.Cleanup(func() { jsonOutput, nonInteractive = oldJSON, oldNonInteractive })
	jsonOutput, nonInteractive = true, false
	if followRolloutByDefault() {
		t.Fatal("followed a rollout with --json")
	}
	jsonOutput, nonInteractive = false, true
	if followRolloutByDefault() {
		t.Fatal("followed a rollout in non-interactive mode")
	}
}
