// Tests for pkg/dashboard/stages — the closed-6-stage renderer
// shared between the CLI post-stream summary (cmd/gregale/deploys_show.go)
// and the dashboard deployment-detail page (cmd/apid/handlers_dashboard.go).
package stages

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// allItems is a convenience builder for a fully-populated
// state.StageState with all 6 stages completed. The order matches
// StageOrder() so the test fixtures stay in lock-step with the
// production wire shape.
func allItems(durationsMs []int64) state.StageState {
	order := StageOrder()
	if len(durationsMs) != len(order) {
		panic("allItems: durations must match StageOrder length")
	}
	now := time.Now().UTC()
	items := make([]state.StageStateItem, 0, len(order))
	for i, name := range order {
		started := now.Add(time.Duration(-(len(order) - i)) * time.Second)
		ended := started.Add(time.Duration(durationsMs[i]) * time.Millisecond)
		items = append(items, state.StageStateItem{
			Name:       name,
			StartedAt:  &started,
			EndedAt:    &ended,
			DurationMs: durationsMs[i],
			Status:     "completed",
		})
	}
	return state.StageState{
		Current: "",
		History: items,
	}
}

// TestStageOrderClosedSet asserts the canonical 6-stage invariant.
// A drift here is a customer-visible bug (the renderer silently
// emits the wrong number of rows); the panic-on-drift guard at
// RenderSummaryText/RenderSummaryHTML catches this at first
// invocation, but the unit test pins the length so a future
// contributor sees the failure in their local red.
func TestStageOrderClosedSet(t *testing.T) {
	if got := len(state.AllStageNames); got != StageOrderClosedSet {
		t.Fatalf("pkg/state.AllStageNames has %d entries, want %d (closed set drift; update StageOrder + migrations/00302 CHECK together)", got, StageOrderClosedSet)
	}
	if got := len(StageOrder()); got != StageOrderClosedSet {
		t.Fatalf("StageOrder() has %d entries, want %d", got, StageOrderClosedSet)
	}
	if got := len(StageLabels()); got != StageOrderClosedSet {
		t.Fatalf("StageLabels() has %d entries, want %d", got, StageOrderClosedSet)
	}
}

// TestStageOrderMatchesAllStageNames asserts the CLI / dashboard
// row order matches the wire-confirmed pkg/state.AllStageNames
// order. A swap here (e.g. snapshot_prepare before image_build)
// would still render 6 rows but the customer's eye would see the
// pipeline running backwards — silent bug.
func TestStageOrderMatchesAllStageNames(t *testing.T) {
	got := StageOrder()
	if len(got) != len(state.AllStageNames) {
		t.Fatalf("StageOrder length %d != AllStageNames %d", len(got), len(state.AllStageNames))
	}
	for i := range got {
		if got[i] != state.AllStageNames[i] {
			t.Fatalf("StageOrder[%d] = %q, want %q (CLIs must stay in lock-step with the wire vocabulary)", i, got[i], state.AllStageNames[i])
		}
	}
}

// TestStageLabelsKeysMatchOrder asserts the label map's keys cover
// exactly StageOrder. A missing label renders as the raw StageName
// constant (better than a panic), but the test pins the contract.
func TestStageLabelsKeysMatchOrder(t *testing.T) {
	labels := StageLabels()
	for _, name := range StageOrder() {
		if _, ok := labels[name]; !ok {
			t.Errorf("StageLabels missing entry for %q", name)
		}
	}
}

// TestGlyphCoversClosedStatusSet asserts every per-status glyph
// returns a non-empty string. The dashboard template relies on
// every row having a glyph; an empty value would render a blank
// cell.
func TestGlyphCoversClosedStatusSet(t *testing.T) {
	for _, status := range []string{"completed", "failed", "in_progress", "pending", ""} {
		if g := Glyph(status); g == "" {
			t.Errorf("Glyph(%q) returned empty string", status)
		}
	}
}

// TestGlyphDistinct asserts the four primary statuses map to
// distinct glyphs. The customer relies on the visual distinction
// (✓ vs ✗ vs … vs ·) to scan the 6-row block at a glance.
func TestGlyphDistinct(t *testing.T) {
	seen := make(map[string]string)
	for _, status := range []string{"completed", "failed", "in_progress", "pending"} {
		g := Glyph(status)
		if prev, ok := seen[g]; ok {
			t.Errorf("Glyph(%q)=%q collides with Glyph(%q)=%q", status, g, prev, g)
		}
		seen[g] = status
	}
}

// TestFormatStageDurationPinsAllBranches pins the three-column
// duration formatter's three branches. A future refactor that
// drops the "…" for in_progress (or the "failed: " prefix for a
// failed stage with a reason) would silently break the CLI's
// column alignment.
func TestFormatStageDurationPinsAllBranches(t *testing.T) {
	cases := []struct {
		name     string
		durMs    int64
		status   string
		reason   string
		wantSub  string
		wantBare string
	}{
		{"completed with dur", 1200, "completed", "", "1.2s", ""},
		{"completed zero dur", 0, "completed", "", "0.0s", ""},
		// Hunt 12 H12-5: the builder-slot wait booked to source_download.
		{"completed with note", 192300, "completed", "waited 3m12s for a builder slot", "", "192.3s (waited 3m12s for a builder slot)"},
		{"in_progress", 0, "in_progress", "", "…", ""},
		{"failed with reason", 0, "failed", "oom in build", "failed: oom in build", ""},
		{"failed no reason", 0, "failed", "", "", "failed"},
		{"pending", 0, "pending", "", "0.0s", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatStageDuration(tc.durMs, tc.status, tc.reason)
			if tc.wantSub != "" && !strings.Contains(got, tc.wantSub) {
				t.Errorf("FormatStageDuration(%d,%q,%q) = %q, want substring %q", tc.durMs, tc.status, tc.reason, got, tc.wantSub)
			}
			if tc.wantBare != "" && got != tc.wantBare {
				t.Errorf("FormatStageDuration(%d,%q,%q) = %q, want %q", tc.durMs, tc.status, tc.reason, got, tc.wantBare)
			}
		})
	}
}

// TestRenderSummaryText_NilWriter asserts the explicit guard.
// A nil writer would panic inside fmt.Fprintf; a returned error
// lets the caller branch cleanly.
func TestRenderSummaryText_NilWriter(t *testing.T) {
	if err := RenderSummaryText(nil, state.StageState{}, "", time.Time{}); err == nil {
		t.Fatalf("RenderSummaryText(nil, ...) returned nil error, want error")
	}
}

// TestRenderSummaryText_AllCompleted asserts the happy-path
// output. Pins row order, label, glyph, and the leading two-space
// pad that keeps the column aligned with the LiveTicker's 24-char
// name column.
func TestRenderSummaryText_AllCompleted(t *testing.T) {
	ss := allItems([]int64{1200, 4800, 8100, 2100, 12600, 400})
	var buf bytes.Buffer
	if err := RenderSummaryText(&buf, ss, "live", time.Now()); err != nil {
		t.Fatalf("RenderSummaryText returned error: %v", err)
	}
	out := buf.String()
	wantLines := []string{
		"✓  Source downloaded",
		"✓  Dependencies restored",
		"✓  Image built",
		"✓  Security scan",
		"✓  Snapshot prepared",
		"✓  Readiness passed",
		"Total:",
		"· live since",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Errorf("RenderSummaryText output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestRenderSummaryText_NoTerminalAt asserts the footer is omitted
// when terminalAt is the zero time. This is the path today's
// `gregale deploys show <id>` (no --status) takes — the wire call
// returns just the stage_state, terminalAt is unknown, so the
// block stops after the total line.
func TestRenderSummaryText_NoTerminalAt(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 1000, 1000, 1000})
	var buf bytes.Buffer
	if err := RenderSummaryText(&buf, ss, "live", time.Time{}); err != nil {
		t.Fatalf("RenderSummaryText returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Total:") {
		t.Errorf("expected Total: line, got %q", out)
	}
	if strings.Contains(out, "live since") {
		t.Errorf("did not expect live since when terminalAt is zero, got %q", out)
	}
	if strings.Contains(out, "failed at") {
		t.Errorf("did not expect failed at when terminalAt is zero, got %q", out)
	}
}

// TestRenderSummaryText_AllPendingNoFooter asserts the CLI/dashboard
// footer-gate symmetry contract (review finding C3):
// when totalMs==0 AND terminalAt is zero (every stage still
// pending, in-flight pre-first-frame) BOTH renderers omit the
// footer entirely. Without this pin, RenderSummaryText emits
// "Total: 0.0s" while RenderSummaryHTML omits the <p> — a silent
// CLI/dashboard drift.
func TestRenderSummaryText_AllPendingNoFooter(t *testing.T) {
	ss := state.StageState{} // empty history, empty current — fully pending.
	var buf bytes.Buffer
	if err := RenderSummaryText(&buf, ss, "live", time.Time{}); err != nil {
		t.Fatalf("RenderSummaryText returned error: %v", err)
	}
	if strings.Contains(buf.String(), "Total:") {
		t.Errorf("expected no Total: line for all-pending + zero terminalAt, got %q", buf.String())
	}
	// Mirror assertion on the HTML renderer.
	html := string(RenderSummaryHTML(ss, "live", time.Time{}))
	if strings.Contains(html, "Total:") {
		t.Errorf("expected no Total: in HTML for all-pending + zero terminalAt, got %q", html)
	}
}

// TestRenderSummaryText_FailedFooter asserts the failed branch
// picks the "failed at" footer text. Mirrors the customer use
// case of a deployment that errored mid-build.
func TestRenderSummaryText_FailedFooter(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 0, 0, 0})
	// Mark the third stage as failed to give the footer a real reason.
	ss.History[2].Status = "failed"
	ss.History[2].Reason = "out of memory"
	terminalAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	var buf bytes.Buffer
	if err := RenderSummaryText(&buf, ss, "failed", terminalAt); err != nil {
		t.Fatalf("RenderSummaryText returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "✗  Image built") {
		t.Errorf("expected failed glyph for image_build, got %q", out)
	}
	if !strings.Contains(out, "failed: out of memory") {
		t.Errorf("expected 'failed: out of memory' duration column, got %q", out)
	}
	if !strings.Contains(out, "· failed at 2026-08-20T12:34:56Z") {
		t.Errorf("expected 'failed at' footer with terminalAt UTC, got %q", out)
	}
}

// TestRenderSummaryHTML_EmptyState asserts the empty-state
// contract. The dashboard template gates on
// `{{ if .Data.Stages.BodyHTML }}` so the empty value must be
// safe to hand to html/template. Cast through template.HTML to
// prove the round-trip is clean.
func TestRenderSummaryHTML_EmptyState(t *testing.T) {
	got := RenderSummaryHTML(state.StageState{}, "", time.Time{})
	if string(got) != "" {
		t.Errorf("RenderSummaryHTML on empty state = %q, want empty", got)
	}
	// The empty template.HTML MUST round-trip through
	// html/template without producing visible output.
	tpl := template.Must(template.New("x").Parse(`{{ . }}`))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, got); err != nil {
		t.Fatalf("template round-trip failed: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("template rendered non-empty output for empty HTML: %q", buf.String())
	}
}

// TestRenderSummaryHTML_AllCompleted asserts the happy-path HTML
// output. Pins the section root class, the row structure, and
// the footer element. The dashboard template inlines the result
// directly so the structure must be stable.
func TestRenderSummaryHTML_AllCompleted(t *testing.T) {
	ss := allItems([]int64{1200, 4800, 8100, 2100, 12600, 400})
	terminalAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	html := string(RenderSummaryHTML(ss, "live", terminalAt))
	wantSubstrings := []string{
		`<section class="stage-timeline">`,
		`<span class="glyph">✓</span>`,
		`<span class="label">Source downloaded</span>`,
		`<span class="duration">1.2s</span>`,
		`<span class="label">Readiness passed</span>`,
		`<p class="stage-footer">Total: 29.2s`,
		`live since 2026-08-20T12:34:56Z`,
		`</section>`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(html, want) {
			t.Errorf("RenderSummaryHTML output missing %q\nfull output:\n%s", want, html)
		}
	}
}

// TestRenderSummaryHTML_RowOrderHardPinned asserts the six rows
// appear in the canonical StageOrder. Catches a future refactor
// that accidentally sorts by label or by some other key.
func TestRenderSummaryHTML_RowOrderHardPinned(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 1000, 1000, 1000})
	html := string(RenderSummaryHTML(ss, "live", time.Time{}))
	labels := StageLabels()
	prev := -1
	for _, name := range StageOrder() {
		needle := `<span class="label">` + labels[name] + `</span>`
		idx := strings.Index(html, needle)
		if idx < 0 {
			t.Errorf("RenderSummaryHTML missing row for %q", name)
			continue
		}
		if idx <= prev {
			t.Errorf("RenderSummaryHTML row for %q (idx %d) appears before previous row (idx %d)", name, idx, prev)
		}
		prev = idx
	}
}

// TestRenderSummaryHTML_FailedFooter asserts the failed branch
// HTML contract. The dashboard template inlines the result
// directly so the failure-mode structure must match the
// success-mode structure (only the footer copy differs).
func TestRenderSummaryHTML_FailedFooter(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 0, 0, 0})
	ss.History[2].Status = "failed"
	ss.History[2].Reason = "out of memory"
	terminalAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	html := string(RenderSummaryHTML(ss, "failed", terminalAt))
	if !strings.Contains(html, `failed: out of memory`) {
		t.Errorf("expected 'failed: out of memory' in rendered HTML, got %q", html)
	}
	if !strings.Contains(html, `failed at 2026-08-20T12:34:56Z`) {
		t.Errorf("expected 'failed at 2026-08-20T12:34:56Z' footer, got %q", html)
	}
}

// TestRenderSummaryHTML_NilTerminalAt asserts the footer is
// omitted when terminalAt is the zero time. Mirrors the
// pre-deployment state where the row exists but the terminal
// status is not yet known.
func TestRenderSummaryHTML_NilTerminalAt(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 1000, 1000, 1000})
	html := string(RenderSummaryHTML(ss, "live", time.Time{}))
	if !strings.Contains(html, `<p class="stage-footer">`) {
		t.Errorf("expected footer p, got %q", html)
	}
	if strings.Contains(html, "live since") {
		t.Errorf("did not expect live since when terminalAt is zero, got %q", html)
	}
}

// TestRenderSummaryHTML_DoesnotEscapeNumeric asserts that the
// numeric duration path is NOT escaped. The fmt.Sprintf in
// RenderSummaryHTML inlines the duration string into the HTML
// via htmlEscape; the numbers are safe so the test confirms the
// unescaped form reaches the output.
func TestRenderSummaryHTML_NumericNotOverEscaped(t *testing.T) {
	ss := allItems([]int64{1500, 0, 0, 0, 0, 0})
	html := string(RenderSummaryHTML(ss, "live", time.Time{}))
	if !strings.Contains(html, `<span class="duration">1.5s</span>`) {
		t.Errorf("expected 1.5s duration, got %q", html)
	}
}

// TestRenderSummaryHTML_RoundTripsThroughTemplate asserts the
// generated HTML survives an html/template round-trip. The
// dashboard template wraps the result in other tags; this test
// guards against a future refactor that introduces an
// html/template-escape that would double-escape the ✓/✗/… glyphs.
func TestRenderSummaryHTML_RoundTripsThroughTemplate(t *testing.T) {
	ss := allItems([]int64{1000, 1000, 1000, 1000, 1000, 1000})
	html := RenderSummaryHTML(ss, "live", time.Time{})
	tpl := template.Must(template.New("stage").Parse(`{{ . }}`))
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, html); err != nil {
		t.Fatalf("template round-trip failed: %v", err)
	}
	if !strings.Contains(buf.String(), `<section class="stage-timeline">`) {
		t.Errorf("template stripped the section root, got %q", buf.String())
	}
}

// TestClosedSetPanicGuard_WouldFireIfDrifted is a meta-test: it
// documents the contract that assertClosedSet() panics. We don't
// invoke it directly (would crash the test process) but the
// helper's behaviour is exercised by every Render call above —
// if a future contributor widens the canonical set without
// updating StageOrder, those tests will panic during setup and
// the failure mode is loud. This test pins the contract via
// documentation so a casual reader sees the linkage.
func TestClosedSetPanicGuardContract(t *testing.T) {
	if StageOrderClosedSet != 6 {
		t.Errorf("StageOrderClosedSet = %d, want 6 (closed set drift; any change must update pkg/state.AllStageNames + migrations/00302 CHECK together)", StageOrderClosedSet)
	}
}
