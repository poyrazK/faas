// Package stages is the single home for the closed-6-stage deploy
// projection that the CLI live ticker, the CLI post-stream summary,
// and the dashboard deployment-detail page all render.
//
// The package exports the canonical stage order, the human-readable
// label map, the per-status glyph table, the duration formatter,
// and two render entry points:
//
//   - RenderSummaryText  writes the closed 6-row block to an io.Writer
//     (CLI post-stream path).
//   - RenderSummaryHTML  returns the same block as template.HTML so
//     the dashboard template only inlines the result (no FuncMap
//     wiring required).
//
// Why a new package (not a method on state.StageState):
// pkg/state owns the typed wire shape but stays free of rendering
// policy — the CLI / dashboard live one layer above it and own
// "what a row looks like". Co-locating the closed-set consts in
// pkg/dashboard/stages keeps the panic-on-drift guard next to the
// renderer that uses it.
//
// Why both CLI and dashboard import from here:
// the CLI's `gregale deploys show <id> --status` and the dashboard's
// /dashboard/apps/{slug}/deployments/{id} page must stay in
// lock-step on labels + glyphs. Mirrors pkg/whycopy/ (cross-binary
// shared rendering vocabulary).
//
// Closed-set invariant (ADR-117 §Consequences):
// The order slice MUST equal pkg/state.AllStageNames; the migrations
// 00302 schema CHECK enforces the same vocabulary at the storage
// layer. A divergence here is a customer-visible bug (the renderer
// silently emits the wrong number of rows). Both Render functions
// panic on drift so the bug surfaces at first invocation rather than
// shipping an off-by-one ticker.
//
// Wiring rules:
//
//   - pkg/dashboard (the views/templates render home) imports this
//     package via the dashboard template inlining approach
//     (template.HTML pre-rendered at handler edge).
//   - cmd/gregale imports this package directly; the in-file
//     renderDeploySummary re-export keeps deploy_stages_test.go
//     unchanged.
//
// What this package is NOT:
//
//   - NOT a writer of stage_state. That authority lives in
//     pkg/state/memstore.go / pgstore.go::AppendDeploymentStage.
//   - NOT a wire transport. cmd/apid/handlers_dashboard.go and
//     cmd/gregale/deploys_show.go own the HTTP round-trip and the
//     json.RawMessage ↔ state.StageState unmarshal.
package stages

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// StageStatus* constants. The server emits these strings verbatim in
// state.StageStateItem.Status (ADR-117 §3 — closed by the migration
// 00302 CHECK constraint). Both render functions and the dashboard
// template use this set as switch arms — keep goconst's "3+
// occurrences" tripwire happy by routing every literal through these
// consts.
const (
	stageStatusInProgress = "in_progress"
	stageStatusCompleted  = "completed"
	stageStatusFailed     = "failed"
	stageStatusPending    = "pending"
)

// StageOrderClosedSet is the canonical length of the deploy-stage
// ticker. MUST equal the closed set in pkg/state.AllStageNames (6)
// AND the SCHEMA CHECK in migrations/00302. A divergence here is a
// customer-visible bug (the renderer silently emits the wrong number
// of rows); both Render functions panic if the slice length drifts
// so the bug surfaces at the first invocation rather than silently
// shipping an off-by-one ticker.
//
// Mirrors cmd/gregale/deploy_stages.go:134 (stageOrderClosedSet) —
// the live ticker keeps its own copy because the ticker is tied to
// the LiveTicker cursor state and is CLI-only.
const StageOrderClosedSet = 6

// StageOrder returns the canonical ordering of the 6 customer-visible
// stages. Source → Snapshot, mirroring the actual build pipeline.
// The slice index is the row index the renderer emits; the server
// emits `event: stage` frames in the same order so the customer's
// eye sees rows light up left-to-right, top-to-bottom.
//
// ADR-117 §3: the order is the same order imaged's
// transitionWithStage chokepoint emits — see
// pkg/imaged/handler.go:1305, 1355, 1551, 1928, 2305, 2334.
func StageOrder() []state.StageName {
	return []state.StageName{
		state.StageSourceDownload,
		state.StageDependencyRestore,
		state.StageImageBuild,
		state.StageSecurityScan,
		state.StageSnapshotPrepare,
		state.StageReadiness,
	}
}

// StageLabels returns the human-readable label for each stage. Kept
// here (not on the wire) so a future UX rename doesn't break the
// server contract. The keys MUST stay in sync with StageOrder() —
// the renderer's row index is the position in StageOrder(), not the
// position in the map, so a missing label renders as the raw
// StageName constant (better than a panic).
func StageLabels() map[state.StageName]string {
	return map[state.StageName]string{
		state.StageSourceDownload:    "Source downloaded",
		state.StageDependencyRestore: "Dependencies restored",
		state.StageImageBuild:        "Image built",
		state.StageSecurityScan:      "Security scan",
		state.StageSnapshotPrepare:   "Snapshot prepared",
		state.StageReadiness:         "Readiness passed",
	}
}

// Glyph returns the per-status glyph for the row prefix. Mirrors
// cmd/gregale/output.go::stageGlyph so the live ticker and the static
// summary use the same constant set. Single home for the glyph
// table — the dashboard template inlines the same glyph per row.
//
//   - "completed"  → ✓
//   - "failed"     → ✗
//   - "in_progress"→ …
//   - default      → · (pending)
func Glyph(status string) string {
	switch status {
	case stageStatusCompleted:
		return "✓"
	case stageStatusFailed:
		return "✗"
	case stageStatusInProgress:
		return "…"
	default:
		return "·"
	}
}

// FormatStageDuration returns the duration column content for one
// row. Three forms:
//
//   - "1.2s"   for completed stages with a known duration
//   - "…"      for in_progress stages (no end time yet)
//   - "failed: <reason>"   for failed stages (overrides duration)
//
// durationMs == 0 on a completed stage renders as "0.0s" so the
// customer sees a sub-second measurement rather than a blank
// cell — builds that resolve in under one tick of the 2s
// statusTicker are common (cache hits, cached snapshots).
//
// ADR-117 §Production-ready follow-on: when called for a failed
// stage, the caller MAY prepend the whycopy-catalogued Title
// (e.g. "Build VM killed (build_oom)") by resolving the
// deployment row's ErrorCode via pkg/whycopy.Decorate BEFORE
// passing the result into this helper. This function takes the
// raw reason string verbatim and does not consult pkg/whycopy —
// keeping pkg/dashboard/stages free of the pkg/whycopy → pkg/api
// import chain (the renderer is the shared formatter and the
// cluster-A seam lives in the caller).
func FormatStageDuration(durationMs int64, status, reason string) string {
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
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// assertClosedSet is the panic-on-drift invariant. Both Render
// functions call this at the top so a future contributor who widens
// pkg/state.AllStageNames (or the migration 00302 CHECK) without
// updating StageOrder() surfaces the bug at first invocation rather
// than silently shipping an off-by-one ticker.
//
// The closed-set is tied to pkg/state.AllStageNames (length 6 at
// mine) — the helper takes the wire-confirmed length and the
// renderer-side length so the message can name both sides.
func assertClosedSet() {
	if want := len(state.AllStageNames); want != StageOrderClosedSet {
		panic(fmt.Sprintf("pkg/dashboard/stages: StageOrderClosedSet=%d, pkg/state.AllStageNames=%d (closed set drift — extend the migration 00302 CHECK and the schema, OR fix the renderer)", StageOrderClosedSet, want))
	}
	if got := len(StageOrder()); got != StageOrderClosedSet {
		panic(fmt.Sprintf("pkg/dashboard/stages: StageOrder()=%d, StageOrderClosedSet=%d (closed set drift — extend the migration 00302 CHECK and the schema, OR fix the renderer)", got, StageOrderClosedSet))
	}
	if got := len(StageLabels()); got != StageOrderClosedSet {
		panic(fmt.Sprintf("pkg/dashboard/stages: StageLabels()=%d, StageOrderClosedSet=%d (closed set drift — labels MUST match order)", got, StageOrderClosedSet))
	}
}

// RenderSummaryText writes the closed 6-stage summary block for a
// terminal-status deployment (live / failed / superseded) to w. It
// is the read-side counterpart to the live ticker: the same row
// order, the same labels, the same duration formatter — only the
// cursor gymnastics are gone.
//
// Layout:
//
//	✓  Source downloaded         1.2s
//	✓  Dependencies restored     4.8s
//	✓  Image built               8.1s
//	✓  Security scan             2.1s
//	✓  Snapshot prepared        12.6s
//	✓  Readiness passed          0.4s
//
//	Total: 29.1s · live since 2026-08-19 18:42 UTC
//
// The "live since" / "failed at" / "<status> at" footer is only
// emitted when terminalAt is set. Pass status as the deployment
// row's `deployments.status` string; the dash variants pick the
// footer branch on "live" / "failed" / anything else.
//
// Footer-gate symmetry (review finding C3): the entire footer
// ("Total: …" plus the status-suffix) is suppressed when
// totalMs == 0 AND terminalAt is the zero time — the in-flight
// pre-first-frame case where every stage is still pending. The
// matching gate in RenderSummaryHTML guarantees the CLI block and
// the dashboard widget read identically on the same input.
//
// The caller should gate on output.Enabled() for TTY vs static —
// the rows use the same column-aligned format the LiveTicker's
// ttyLiveTicker emits, so a non-TTY caller that wants the raw text
// path should bypass this for writeStaticLiteOnePerStage. For the
// customer use case (TTY or pipe), the alignment is readable in
// both modes — pipe output looks the same, just without redraw
// escapes.
func RenderSummaryText(w io.Writer, ss state.StageState, status string, terminalAt time.Time) error {
	if w == nil {
		return fmt.Errorf("RenderSummaryText: nil writer")
	}
	assertClosedSet()

	order := StageOrder()
	labels := StageLabels()

	// Build a name → StageStateItem lookup so a missing history
	// entry (which can happen mid-deploy, before the first frame
	// arrives) renders as the canonical "pending" row rather than
	// a panic.
	byName := make(map[state.StageName]state.StageStateItem, len(ss.History))
	for _, item := range ss.History {
		byName[item.Name] = item
	}

	var totalMs int64
	for _, name := range order {
		item, ok := byName[name]
		var (
			rowStatus string
			durMs     int64
			reason    string
		)
		switch {
		case !ok && ss.Current == name:
			// The active stage hasn't been pushed to history yet;
			// render it as in_progress with the started-at delta.
			rowStatus = stageStatusInProgress
			if ss.CurrentStartedAt != nil {
				durMs = time.Since(*ss.CurrentStartedAt).Milliseconds()
				if durMs < 0 {
					durMs = 0
				}
			}
		case ok:
			rowStatus = item.Status
			durMs = item.DurationMs
			reason = item.Reason
		default:
			rowStatus = stageStatusPending
		}
		glyph := Glyph(rowStatus)
		label, ok := labels[name]
		if !ok {
			label = string(name)
		}
		dur := FormatStageDuration(durMs, rowStatus, reason)
		if _, err := fmt.Fprintf(w, "  %s  %-22s %s\n", glyph, label, dur); err != nil {
			return fmt.Errorf("RenderSummaryText: write row %s: %w", name, err)
		}
		// Total wall-clock: sum of completed stages' DurationMs
		// PLUS the in_progress stage's running delta. Pending
		// stages don't contribute.
		if rowStatus == stageStatusCompleted {
			totalMs += durMs
		} else if rowStatus == stageStatusInProgress && durMs > 0 {
			totalMs += durMs
		}
	}

	// Footer gate (matches RenderSummaryHTML): only emit the
	// "Total: …" / "<status> at <ts>" line when there's something
	// meaningful to report. totalMs==0 && terminalAt.IsZero()
	// means every stage is still pending (in-flight pre-first-frame
	// or an entirely empty StageState that the caller still
	// invoked Render* on) — Text and HTML must agree on whether
	// the footer is suppressed so the two surfaces stay in
	// lock-step. Keeping the CLI and dashboard reading identically
	// on the same row.
	if totalMs > 0 || !terminalAt.IsZero() {
		if _, err := fmt.Fprintf(w, "\n  Total: %s", FormatStageDuration(totalMs, stageStatusCompleted, "")); err != nil {
			return fmt.Errorf("RenderSummaryText: write total: %w", err)
		}
		if !terminalAt.IsZero() {
			switch status {
			case "live":
				if _, err := fmt.Fprintf(w, " · live since %s", terminalAt.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("RenderSummaryText: write live since: %w", err)
				}
			case stageStatusFailed:
				if _, err := fmt.Fprintf(w, " · failed at %s", terminalAt.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("RenderSummaryText: write failed at: %w", err)
				}
			default:
				// superseded / cancelled — render the raw status as
				// a hint so the customer knows why their terminal
				// row didn't carry the ✓ Deployed line.
				if _, err := fmt.Fprintf(w, " · %s at %s", status, terminalAt.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("RenderSummaryText: write %s at: %w", status, err)
				}
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return fmt.Errorf("RenderSummaryText: write footer newline: %w", err)
		}
	}
	return nil
}

// RenderSummaryHTML returns the same closed-6-stage block as
// RenderSummaryText, wrapped in a <section class="stage-timeline">
// root so the dashboard template can inline it directly via
// {{ .Data.Stages.BodyHTML }} without any FuncMap wiring.
//
// Returns the empty template.HTML ("") when the StageState is empty
// — the column is NOT NULL DEFAULT but a brand-new deployment
// (pre-first-frame) can still be observed with a zero history. The
// dashboard template uses `{{ if .Data.Stages.BodyHTML }}` to gate
// the section; the empty value safely renders as nothing.
//
// Row markup:
//
//	<div class="stage-row">
//	  <span class="glyph">✓</span>
//	  <span class="label">Source downloaded</span>
//	  <span class="duration">1.2s</span>
//	</div>
//
// Footer markup (only when totalMs > 0 OR terminalAt is set):
//
//	<p class="stage-footer">Total: 29.1s · live since 2026-08-19 18:42 UTC</p>
//
// Footer-gate symmetry (review finding C3): the entire <p> is
// suppressed when totalMs == 0 AND terminalAt is the zero time —
// the in-flight pre-first-frame case where every stage is still
// pending. The matching gate in RenderSummaryText guarantees the
// CLI block and the dashboard widget read identically on the same
// input.
//
// Caller is responsible for the surrounding <h2>Stages</h2> heading
// (the template owns that — this package only emits the content).
func RenderSummaryHTML(ss state.StageState, status string, terminalAt time.Time) template.HTML {
	assertClosedSet()
	// Empty stage_state → empty HTML (template omits the section).
	if len(ss.History) == 0 && ss.Current == "" {
		return template.HTML("")
	}

	order := StageOrder()
	labels := StageLabels()

	byName := make(map[state.StageName]state.StageStateItem, len(ss.History))
	for _, item := range ss.History {
		byName[item.Name] = item
	}

	var rowsHTML strings.Builder
	if ss.RetryRestartReason != "" {
		fmt.Fprintf(&rowsHTML, "<p class=\"stage-retry-note\">%s</p>", template.HTMLEscapeString(ss.RetryRestartReason))
	}
	var totalMs int64
	for _, name := range order {
		item, ok := byName[name]
		var (
			rowStatus string
			durMs     int64
			reason    string
		)
		switch {
		case !ok && ss.Current == name:
			rowStatus = stageStatusInProgress
			if ss.CurrentStartedAt != nil {
				durMs = time.Since(*ss.CurrentStartedAt).Milliseconds()
				if durMs < 0 {
					durMs = 0
				}
			}
		case ok:
			rowStatus = item.Status
			durMs = item.DurationMs
			reason = item.Reason
		default:
			rowStatus = stageStatusPending
		}
		glyph := htmlEscape(Glyph(rowStatus))
		label, ok := labels[name]
		if !ok {
			label = string(name)
		}
		labelSafe := htmlEscape(label)
		durSafe := htmlEscape(FormatStageDuration(durMs, rowStatus, reason))
		fmt.Fprintf(&rowsHTML,
			`<div class="stage-row"><span class="glyph">%s</span><span class="label">%s</span><span class="duration">%s</span></div>`,
			glyph, labelSafe, durSafe,
		)
		if rowStatus == stageStatusCompleted {
			totalMs += durMs
		} else if rowStatus == stageStatusInProgress && durMs > 0 {
			totalMs += durMs
		}
	}

	var footerHTML string
	if totalMs > 0 || !terminalAt.IsZero() {
		totalStr := FormatStageDuration(totalMs, stageStatusCompleted, "")
		var tail string
		if !terminalAt.IsZero() {
			switch status {
			case "live":
				tail = fmt.Sprintf(" · live since %s", terminalAt.UTC().Format(time.RFC3339))
			case stageStatusFailed:
				tail = fmt.Sprintf(" · failed at %s", terminalAt.UTC().Format(time.RFC3339))
			default:
				tail = fmt.Sprintf(" · %s at %s", status, terminalAt.UTC().Format(time.RFC3339))
			}
		}
		footerHTML = fmt.Sprintf(`<p class="stage-footer">Total: %s%s</p>`, htmlEscape(totalStr), tail)
	}

	var final strings.Builder
	final.WriteString(`<section class="stage-timeline">`)
	final.WriteString(rowsHTML.String())
	final.WriteString(footerHTML)
	final.WriteString(`</section>`)
	//nolint:gosec // G203: final.String() is a fixed-shape stage-timeline
	// block built from operator-owned labels (compile-time constants in
	// StageLabels()), constant glyphs (Glyph()), numeric durations
	// rendered via FormatStageDuration(), escaped terminalAt via
	// time.RFC3339, and an escaped image-side reason slot (imaged
	// failure messages run through htmlEscape above). No raw
	// customer-supplied HTML flows through. The template.HTML cast is
	// load-bearing — it sidesteps html/template escaping for the
	// pre-rendered block so the dashboard template can inline it via
	// {{ .BodyHTML }} without Funcmap plumbing.
	return template.HTML(final.String())
}

// htmlEscape is the minimal in-this-file escaper. We can't pull in
// html.EscapeString without a circular concern (the package already
// imports html/template, but EscapeString returns a different
// string type). The labels are operator-owned and ASCII; the
// duration values are either numeric ("1.2s") or "failed: <reason>".
// <reason> is the only field that could carry user-influenced input
// (imaged's failure messages). The escape is intentionally a
// conservative HTML-text set, not a full HTML5 attribute escape.
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
