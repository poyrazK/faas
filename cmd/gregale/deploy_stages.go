// Deploy stage renderer — ADR-117 §3. Maps the closed 6-stage
// jsonb vocabulary emitted by the server's `event: stage` SSE
// frames onto the CLI's human-readable ticker.
//
// Layering:
//
//	streamDeployLogs (commands2.go)
//	  └── renderStageTicker (this file)
//	      └── LiveTicker (output.go)
//
// This file is the CLI-side adapter around the shared
// `pkg/dashboard/stages` renderer. The shared package owns the
// canonical stage order, the human label map, the duration
// formatter, and the closed-set panic guard (every component
// that renders the 6-stage block — CLI live ticker, CLI
// post-stream summary, dashboard timeline widget — imports from
// there so the labels stay in lock-step with the server-side
// consts).
//
// What's local to this file (and why):
//   - the live ticker (renderStageTicker / HandleStageFrame / Close)
//     is CLI-only state: it carries the LiveTicker cursor + the
//     per-row in-memory cache. The dashboard widget renders the
//     static block via pkg/dashboard/stages.RenderSummaryHTML; it
//     has no need for a LiveTicker.
//   - the package-level re-exports (stageOrder, stageLabels,
//     stageStatusInProgress, stageStatusCompleted, stageStatusFailed,
//     stageStatusPending, stageOrderClosedSet) keep
//     output.go::stageGlyph, output_test.go, commands2_test.go, and
//     the live ticker all compiling without churn. The values are
//     the same — the single source of truth is now
//     pkg/dashboard/stages.

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard/stages"
	"github.com/onebox-faas/faas/pkg/state"
)

// Stage-status constants. Re-exported from pkg/dashboard/stages so
// the in-file references (renderStageTicker, formatStageDuration,
// output.go::stageGlyph) keep compiling without alias gymnastics.
// The server emits these strings verbatim in `event: stage` JSON
// frames (ADR-117 §3); the CLI uses them as map keys + switch arms.
//
// NOTE: the values are intentionally the same as the package-level
// consts in pkg/dashboard/stages. If a future contributor widens
// the vocabulary, that package is the single source of truth —
// these re-exports are frozen.
const (
	stageStatusInProgress = "in_progress"
	stageStatusCompleted  = "completed"
	stageStatusFailed     = "failed"
	stageStatusPending    = "pending"
)

// stageOrder is the canonical ordering of the 6 customer-visible
// stages. Re-exported from pkg/dashboard/stages.StageOrder() so the
// in-package callers (renderStageTicker, output.go::padName/row) keep
// treating this as a slice; the shared package's StageOrder() returns
// a fresh slice each call (cheap — 6 entries) but the cli-side
// callers were written against a stable var to match the old
// shape. The values are the same.
var stageOrder = stages.StageOrder()

// stageLabels re-exports the shared label map. Same rationale as
// stageOrder.
var stageLabels = stages.StageLabels()

// stageOrderClosedSet is the canonical length of the deploy-stage
// ticker. Re-exported from pkg/dashboard/stages.StageOrderClosedSet.
// The renderStageTicker panic guard (below) AND the renderDeploySummary
// wrapper (also below) depend on this const for the drift check.
const stageOrderClosedSet = stages.StageOrderClosedSet

// renderDeploySummary writes the closed 6-stage summary block to w.
// CLI-side thin wrapper around pkg/dashboard/stages.RenderSummaryText.
// The signature is preserved (io.Writer, state.StageState, string,
// time.Time -> error) so deploy_stages_test.go and the existing
// streamDeployLogs end-of-stream call site keep compiling without
// churn.
//
// Why a wrapper (not a direct call):
//   - keeps the CLI's existing test file untouched (signature
//     stability — Risk §1).
//   - isolates the import direction (cmd/gregale → pkg/dashboard/stages)
//     to a single adapter file so future CLI-side refactors can
//     swap the renderer without touching every call site.
func renderDeploySummary(w io.Writer, ss state.StageState, status string, terminalAt time.Time) error {
	return stages.RenderSummaryText(w, ss, status, terminalAt)
}

// renderStageTicker constructs the per-deploy ticker. The row count
// is derived from `stageOrder`; the function panics if
// `stageOrder` no longer matches the closed stage set
// (stageOrderClosedSet). This is intentional: the contract is
// "render the closed stage set" and a divergence is a programming
// error the CLI binary must refuse to boot with.
//
// ADR-117 §3 + PR-A review fix (F5): streamDeployLogs
// (commands2.go) constructs ONE stageTicker per deployment, NOT
// per SSE frame. The ticker outlives the SSE decoder's frame loop
// so the caller can drive Update across many frames without
// re-allocating.
func renderStageTicker(w io.Writer) *stageTicker {
	if len(stageOrder) != stageOrderClosedSet {
		panic(fmt.Sprintf("renderStageTicker: stageOrder has %d entries, want %d (closed set in pkg/state.AllStageNames + migrations/00302)", len(stageOrder), stageOrderClosedSet))
	}
	if len(stageLabels) != stageOrderClosedSet {
		panic(fmt.Sprintf("renderStageTicker: stageLabels has %d entries, want %d", len(stageLabels), stageOrderClosedSet))
	}
	rows := make([]stageRow, len(stageOrder))
	for i, name := range stageOrder {
		rows[i] = stageRow{name: name, status: stageStatusPending, durMs: 0}
	}
	idx := make(map[state.StageName]int, len(stageOrder))
	for i, name := range stageOrder {
		idx[name] = i
	}
	return &stageTicker{
		w:     w,
		rows:  rows,
		tick:  NewLiveTicker(w, len(stageOrder)),
		index: idx,
	}
}

// stageRow is one row's in-memory mirror. The ticker caches
// (name, status, duration) so successive SSE frames can refresh
// the row without re-fetching the typed state. The struct is
// CLI-only — the dashboard path doesn't need an in-memory cache,
// it renders the static block once per HTTP request.
type stageRow struct {
	name   state.StageName
	status string
	durMs  int64
}

// stageTicker is the in-memory mirror of the rendered block.
// Separate from LiveTicker so tests can assert the state without
// parsing ANSI escapes.
type stageTicker struct {
	w     io.Writer
	rows  []stageRow
	tick  LiveTicker
	index map[state.StageName]int
}

// HandleStageFrame applies one decoded SSE `event: stage` payload
// to the renderer. The payload is the same JSON shape the server
// emits at cmd/apid/handlers_ext.go::emitStageFrame — see that
// doc block for the wire spec.
//
// `name` is the StageName string, `status` is one of
// "in_progress" / "completed" / "failed", `durationMs` is the
// server-measured elapsed time, `reason` is the failure on "failed" and an
// optional note on "completed" (the builder-slot wait). Unknown names are
// silently dropped (a strict server
// should never emit one, but the renderer is tolerant so a future
// stage added by the server doesn't break older CLIs).
func (t *stageTicker) HandleStageFrame(name, status string, durationMs int64, reason string) {
	if t == nil {
		return
	}
	rowIdx, ok := t.index[state.StageName(name)]
	if !ok {
		return
	}
	t.rows[rowIdx].status = status
	t.rows[rowIdx].durMs = durationMs
	if status == stageStatusFailed && reason != "" {
		t.rows[rowIdx].durMs = 0 // collapse duration on failure; the reason takes the column
	}
	t.tick.Update(rowIdx,
		stageLabels[state.StageName(name)],
		status,
		formatStageDuration(t.rows[rowIdx].durMs, status, reason),
	)
}

// Close finalises the ticker. After Close, subsequent
// HandleStageFrame calls are no-ops; the customer's terminal now
// shows the static final block plus whatever the caller prints
// next (the existing `✓ Deployed. …` line in streamDeployLogs).
func (t *stageTicker) Close() {
	if t == nil {
		return
	}
	t.tick.Close()
}

// formatStageDuration returns the duration column content for one
// row. Three forms:
//
//   - "  1.2s"  for completed stages with a known duration
//   - "  …"    for in_progress stages (no end time yet)
//   - "failed: <reason>"  for failed stages (overrides duration)
//
// The leading two-space pad keeps the column aligned with the
// TTY ticker's 24-char name column. durationMs == 0 on a completed
// stage is rendered as "  0.0s" so the customer sees a sub-second
// measurement rather than a blank cell — builds that resolve in
// under one tick of the 2s statusTicker are common (cache hits,
// cached snapshots).
//
// NOTE: this is a CLI-local helper that intentionally pads with
// two leading spaces (the LiveTicker column alignment). The
// shared pkg/dashboard/stages.FormatStageDuration omits the pad
// because the dashboard template emits the pad via CSS.
func formatStageDuration(durationMs int64, status, reason string) string {
	switch status {
	case stageStatusFailed:
		if reason != "" {
			return fmt.Sprintf("failed: %s", reason)
		}
		return stageStatusFailed
	case stageStatusInProgress:
		return "…"
	default:
		// completed / pending — always render the duration so the
		// column is never blank.
		d := time.Duration(durationMs) * time.Millisecond
		// A completed stage may carry a note, such as the builder-slot
		// wait booked to source_download (Hunt 12 H12-5).
		if status == stageStatusCompleted && reason != "" {
			return fmt.Sprintf("%.1fs (%s)", d.Seconds(), reason)
		}
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// stageGlyph is defined in output.go (single home for the per-status
// glyph table — both the live ticker and the static summary render
// use the same constant set). Imported via package main.
