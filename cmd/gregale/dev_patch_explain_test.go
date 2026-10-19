package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevPatchExplanationCoversActionableReasons(t *testing.T) {
	for _, reason := range []string{
		api.DevPatchReasonBuildCommand, api.DevPatchReasonNotRailpack, api.DevPatchReasonNoSourceLayer,
		api.DevPatchReasonSourceNotDeployed, api.DevPatchReasonPlanUnreadable, api.DevPatchReasonRebuildInput,
		api.DevPatchReasonUnsupportedEntry, api.DevPatchReasonTooLarge, api.DevPatchReasonUnsupportedVersion,
	} {
		if why, ok := main.DevPatchExplanation(api.DevPatchPreview{Reason: reason}, ""); !ok || why == "" {
			t.Errorf("reason %q has no explanation", reason)
		}
	}
	// Expected on a first sync or right after enabling live patches: quiet.
	for _, reason := range []string{api.DevPatchReasonNoLiveBuild, api.DevPatchReasonNoBaseManifest, api.DevPatchReasonFullSnapshot, "future_reason"} {
		if _, ok := main.DevPatchExplanation(api.DevPatchPreview{Reason: reason}, ""); ok {
			t.Errorf("reason %q should stay quiet", reason)
		}
	}
	// --watch is suggested only where it can pick a development command.
	if why, _ := main.DevPatchExplanation(api.DevPatchPreview{Reason: api.DevPatchReasonBuildCommand}, "npm run dev"); !strings.Contains(why, "gregale dev --watch") || !strings.Contains(why, "(npm run dev)") {
		t.Fatalf("build-step explanation = %q, want the --watch suggestion", why)
	}
	if why, _ := main.DevPatchExplanation(api.DevPatchPreview{Reason: api.DevPatchReasonBuildCommand}, ""); strings.Contains(why, "--watch") {
		t.Fatalf("build-step explanation = %q suggests watch mode for an app it cannot run", why)
	}
	why, _ := main.DevPatchExplanation(api.DevPatchPreview{Reason: api.DevPatchReasonTooLarge, ChangedPaths: 312, PatchBytes: 9 << 20}, "")
	if !strings.Contains(why, "312 files, 9.0 MiB") || !strings.Contains(why, "200 files, 8.0 MiB") {
		t.Fatalf("too-large explanation = %q", why)
	}
}

func TestDevPatchExplainerShowsEachReasonOnceUntilTheOutcomeChanges(t *testing.T) {
	build := &api.DevPatchPreview{Reason: api.DevPatchReasonBuildCommand}
	input := &api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput}
	eligible := &api.DevPatchPreview{Eligible: true, ChangedPaths: 1}
	var e main.DevPatchExplainer
	steps := []struct {
		preview *api.DevPatchPreview
		shown   bool
	}{
		{&api.DevPatchPreview{Reason: api.DevPatchReasonNoLiveBuild}, false}, // first sync
		{build, true},
		{build, false}, // same reason on every save: once
		{eligible, false},
		{input, true},
		{eligible, false},
		{input, true}, // again after a patched sync
		{nil, false},  // older control plane: no preview
	}
	for i, step := range steps {
		line := e.Explain(step.preview)
		if (line != "") != step.shown {
			t.Fatalf("step %d: line %q, want shown=%t", i, line, step.shown)
		}
		if line != "" && !strings.HasPrefix(line, "Full rebuild instead of a live patch: ") {
			t.Fatalf("step %d: line %q", i, line)
		}
	}
}
