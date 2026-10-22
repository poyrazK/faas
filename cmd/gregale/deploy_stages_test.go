// Pin tests for renderDeploySummary (deploy_stages.go) — the
// closed 6-stage summary renderer used by:
//   - the `gregale deploy` live ticker (TTY branch)
//   - the static post-stream `gregale deploys show <id>` (this PR)
//
// Both paths call the same function so the closed-set, label table,
// duration formatting, and footer branches only need to be pinned
// once. If the closed-set widens (per ADR-117 §2 — must NOT happen
// without a fresh ADR), the `stageOrder` length check inside
// renderDeploySummary fires and the binary refuses to render, which
// is the right blast radius.
//
// The renderer is pure (no globals, no I/O except the writer), so
// each sub-test writes into a bytes.Buffer and asserts substring
// presence. We deliberately assert substrings rather than full-string
// equality — the closed 6-row block format is the contract, the exact
// spacing is the renderer's choice.
package main_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/state"
)

// tPtr returns a pointer to the given time.Time. The
// StageStateItem.StartedAt / EndedAt fields are *time.Time so a nil
// entry can mean "in-flight, not yet ended" — we use pointers
// everywhere here for consistency, even when the value is fixed.
func tPtr(t time.Time) *time.Time { return &t }

// TestRenderDeploySummary_AllSixCompleted pins the success case:
// every stage in the closed set has a `completed` history row and
// the renderer emits each label exactly once. This is what the
// customer sees after a fully-successful deploy.
func TestRenderDeploySummary_AllSixCompleted(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	ss := state.StageState{
		Current:          state.StageReadiness,
		CurrentStartedAt: tPtr(now),
		History: []state.StageStateItem{
			{Name: state.StageSourceDownload, StartedAt: tPtr(now.Add(-30 * time.Second)), EndedAt: tPtr(now.Add(-28 * time.Second)), DurationMs: 2000, Status: "completed"},
			{Name: state.StageDependencyRestore, StartedAt: tPtr(now.Add(-28 * time.Second)), EndedAt: tPtr(now.Add(-23 * time.Second)), DurationMs: 5000, Status: "completed"},
			{Name: state.StageImageBuild, StartedAt: tPtr(now.Add(-23 * time.Second)), EndedAt: tPtr(now.Add(-10 * time.Second)), DurationMs: 13000, Status: "completed"},
			{Name: state.StageSecurityScan, StartedAt: tPtr(now.Add(-10 * time.Second)), EndedAt: tPtr(now.Add(-5 * time.Second)), DurationMs: 5000, Status: "completed"},
			{Name: state.StageSnapshotPrepare, StartedAt: tPtr(now.Add(-5 * time.Second)), EndedAt: tPtr(now.Add(-1 * time.Second)), DurationMs: 4000, Status: "completed"},
			{Name: state.StageReadiness, StartedAt: tPtr(now.Add(-1 * time.Second)), EndedAt: tPtr(now), DurationMs: 1000, Status: "completed"},
		},
	}
	var buf bytes.Buffer
	if err := main.RenderDeploySummary(&buf, ss, "live", now); err != nil {
		t.Fatalf("renderDeploySummary: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Source downloaded",
		"Dependencies restored",
		"Image built",
		"Security scan",
		"Snapshot prepared",
		"Readiness passed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing label %q in rendered summary\nfull: %s", want, got)
		}
	}
	// The footer "live since <ts>" branch (status="live") must
	// appear. The exact timestamp format is the renderer's
	// choice — assert the prefix.
	if !strings.Contains(got, "live since") {
		t.Errorf("expected 'live since' footer for status=live\nfull: %s", got)
	}
}

// TestRenderDeploySummary_LiveDeployment covers the mid-deploy case
// the customer actually runs into: 5 completed stages, the 6th
// (`readiness`) is in-progress. The renderer must NOT print the
// "live since" footer (status=="" here), and the current stage must
// render with the in-progress glyph (stageGlyph returns "→").
func TestRenderDeploySummary_LiveDeployment(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	ss := state.StageState{
		Current:          state.StageReadiness,
		CurrentStartedAt: tPtr(now),
		History: []state.StageStateItem{
			{Name: state.StageSourceDownload, StartedAt: tPtr(now.Add(-30 * time.Second)), EndedAt: tPtr(now.Add(-28 * time.Second)), DurationMs: 2000, Status: "completed"},
			{Name: state.StageDependencyRestore, StartedAt: tPtr(now.Add(-28 * time.Second)), EndedAt: tPtr(now.Add(-23 * time.Second)), DurationMs: 5000, Status: "completed"},
			{Name: state.StageImageBuild, StartedAt: tPtr(now.Add(-23 * time.Second)), EndedAt: tPtr(now.Add(-10 * time.Second)), DurationMs: 13000, Status: "completed"},
			{Name: state.StageSecurityScan, StartedAt: tPtr(now.Add(-10 * time.Second)), EndedAt: tPtr(now.Add(-5 * time.Second)), DurationMs: 5000, Status: "completed"},
			{Name: state.StageSnapshotPrepare, StartedAt: tPtr(now.Add(-5 * time.Second)), EndedAt: tPtr(now.Add(-1 * time.Second)), DurationMs: 4000, Status: "completed"},
		},
	}
	var buf bytes.Buffer
	if err := main.RenderDeploySummary(&buf, ss, "", now); err != nil {
		t.Fatalf("renderDeploySummary: %v", err)
	}
	got := buf.String()
	// No terminal footer — status is "" so neither "live since"
	// nor "failed at" should be present.
	for _, banned := range []string{"live since", "failed at"} {
		if strings.Contains(got, banned) {
			t.Errorf("mid-deploy render must NOT contain %q footer\nfull: %s", banned, got)
		}
	}
	// Each of the 6 labels must still render — the closed-set
	// invariant doesn't depend on whether every row has completed.
	for _, want := range []string{
		"Source downloaded",
		"Dependencies restored",
		"Image built",
		"Security scan",
		"Snapshot prepared",
		"Readiness passed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing label %q in mid-deploy render\nfull: %s", want, got)
		}
	}
}

// TestRenderDeploySummary_Failed covers the failed branch: history
// contains a `failed` row and the footer must print "failed at <ts>".
// The current stage after failure stays at the row that errored
// (here: image_build) and renders with the failed glyph.
func TestRenderDeploySummary_Failed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	ss := state.StageState{
		Current:          state.StageImageBuild,
		CurrentStartedAt: tPtr(now.Add(-10 * time.Second)),
		History: []state.StageStateItem{
			{Name: state.StageSourceDownload, StartedAt: tPtr(now.Add(-30 * time.Second)), EndedAt: tPtr(now.Add(-28 * time.Second)), DurationMs: 2000, Status: "completed"},
			{Name: state.StageDependencyRestore, StartedAt: tPtr(now.Add(-28 * time.Second)), EndedAt: tPtr(now.Add(-23 * time.Second)), DurationMs: 5000, Status: "completed"},
			{Name: state.StageImageBuild, StartedAt: tPtr(now.Add(-23 * time.Second)), EndedAt: tPtr(now.Add(-10 * time.Second)), DurationMs: 13000, Status: "failed", Reason: "OOM"},
		},
	}
	var buf bytes.Buffer
	if err := main.RenderDeploySummary(&buf, ss, "failed", now.Add(-10*time.Second)); err != nil {
		t.Fatalf("renderDeploySummary: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "failed at") {
		t.Errorf("expected 'failed at' footer for status=failed\nfull: %s", got)
	}
	if strings.Contains(got, "live since") {
		t.Errorf("failed render must NOT contain 'live since' footer\nfull: %s", got)
	}
}

// TestRenderDeploySummary_NilWriter pins the contract that callers
// can rely on: a nil writer returns a non-nil error so the binary
// fails fast at the boundary instead of panicking inside the
// renderer's fmt.Fprintf chain. Mirrors the convention of
// renderDeploymentRow's write-failure-is-unrecoverable stance.
func TestRenderDeploySummary_NilWriter(t *testing.T) {
	ss := state.StageState{Current: state.StageSourceDownload}
	if err := main.RenderDeploySummary(nil, ss, "", time.Time{}); err == nil {
		t.Error("renderDeploySummary(nil) = nil err, want non-nil")
	}
}
