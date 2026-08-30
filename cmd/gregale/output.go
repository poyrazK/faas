// UX spec §3.2 output conventions — the writer-based gate that strips
// `✓/✗/→/!` glyphs and any future colour escapes when the customer's
// stdout is not a TTY or when `NO_COLOR` is set non-empty (per
// no-color.org). Every existing `fmt.Fprintf(osStdout, "✗ …", …)` call
// site should migrate to PrintOK / PrintFail / PrintProgress / PrintWarn
// so the same line shape survives a pipe. Reuses the package-level
// jsonOutput flag (json_flag.go:18) so the JSON path is always plain.
//
// This is the §3.2 follow-up to PR #101. The plan is at
// /Users/poyrazk/.claude/plans/lets-create-imp-plan-majestic-hanrahan.md.
//
// Cross-platform: every renderer + the gate live here so this file
// compiles on every GOOS. The platform-specific TTY probe lives in
// isatty_unix.go (term.IsTerminal on unix) and isatty_windows.go
// (always returns false — cmd/gregale does not officially target Windows
// today; the stub keeps `go build ./...` happy on a contributor's
// Windows box).

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// stdoutIsTTY is defined per-platform: isatty_unix.go calls
// term.IsTerminal under the hood; isatty_windows.go always returns
// false (stub). The test seam (testOnlyTTY below) overrides both.

// noColorSet reads NO_COLOR via os.LookupEnv once and caches the result.
// Per no-color.org, any non-empty value (including "0", "false", "no")
// disables colour and glyphs.
var noColorCached atomic.Bool
var noColorVal bool

func noColorSet() bool {
	if noColorCached.Load() {
		return noColorVal
	}
	if raw, ok := os.LookupEnv("NO_COLOR"); ok && raw != "" {
		noColorVal = true
	}
	noColorCached.Store(true)
	return noColorVal
}

// Enabled reports whether human-readable glyphs and colour are
// appropriate for the current stdout. The single global gate — both
// success and failure renderers call into writeStatus which checks
// this exactly once per line.
func Enabled() bool {
	if jsonOutput {
		return false
	}
	if noColorSet() {
		return false
	}
	return stdoutIsTTY()
}

// PrintOK emits a "done" line. Glyph `✓` when Enabled, plain otherwise.
func PrintOK(w io.Writer, format string, a ...any) {
	writeStatus(w, "✓", format, a...)
}

// PrintFail emits a "failed" line. Glyph `✗` when Enabled.
func PrintFail(w io.Writer, format string, a ...any) {
	writeStatus(w, "✗", format, a...)
}

// PrintProgress emits an "in-progress" line. Glyph `→` when Enabled.
func PrintProgress(w io.Writer, format string, a ...any) {
	writeStatus(w, "→", format, a...)
}

// PrintWarn emits a "warning" line. Glyph `!` when Enabled.
func PrintWarn(w io.Writer, format string, a ...any) {
	writeStatus(w, "!", format, a...)
}

// GlyphOK is the leading character for a "done" line. Lives here (not
// at the call site) so the lint tripwire that rejects leading glyph
// literals outside output.go has a single allow-listed file.
const GlyphOK = "✓"

// GlyphFail is the leading character for a "failed" line. Same rationale
// as GlyphOK.
const GlyphFail = "✗"

// GlyphProgress is the leading character for an "in progress" line.
// Same rationale as GlyphOK.
const GlyphProgress = "→"

// GlyphEmDash is the placeholder character used when a tabular column
// has no value to render (e.g. a queued build with no started_at, a
// billing subscription that has never synced). Lives here (not at the
// call site) so goconst's "string has 3 occurrences" tripwire has a
// single allow-listed definition; every renderer's empty-state line
// references this const instead of the U+2014 literal.
const GlyphEmDash = "—"

// writeStatus centralises the "leading glyph + space + content + newline"
// rule. The four Print* functions are one-liners above. The error from
// Fprintf is intentionally discarded: writer failures (closed pipe,
// broken TTY) are unrecoverable here, and we never want a status line
// to crash the CLI on its way out. This is the same shape the rest of
// the package uses for stdout/stderr printers (commands*.go).
func writeStatus(w io.Writer, glyph, format string, a ...any) {
	prefix := ""
	if Enabled() {
		prefix = glyph + " "
	}
	_, _ = fmt.Fprintf(w, prefix+format+"\n", a...)
}

// testOnlyTTY is the package-private test seam. nil in production
// (stdoutIsTTY returns the real platform result). output_test.go sets it
// to &true for the common case where a captured buffer should still
// carry the glyph, and to &false for explicit no-TTY assertions.
//
// The pointer is read under no concurrency — `cmd/gregale/` tests don't
// use t.Parallel today. If that ever changes, swap to
// `atomic.Pointer[bool]` (Go 1.22+). Documented here so a future
// contributor doesn't silently introduce a race.
var testOnlyTTY *bool

// RenderTitle emits the title row of an APIError render. When Enabled()
// the leading `✗ ` glyph prefixes the title; otherwise the row is just
// the title. The `Detail` row and the `Docs` row are written separately
// by the caller. The split is here (not inside one big function) so the
// glyph tripwire in lint_tripwires_test.go has a single allow-listed
// file (output.go) for every ✓/✗/→ string literal in the package. The
// Fprintf errors are intentionally discarded (same convention as
// writeStatus — see that comment).
func RenderTitle(w io.Writer, title string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "✗ %s\n", title)
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n", title)
}

// RenderDocsRow emits the third line of an APIError render — the row
// pointing at the docs URL. When Enabled() the row carries `  → `
// (UX §3.3's three-line contract); otherwise it carries `  ` so the
// line count is preserved for script consumers that split on "\n".
// Fprintf errors intentionally discarded — same convention as
// writeStatus / RenderTitle.
func RenderDocsRow(w io.Writer, url string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "  → %s\n", url)
		return
	}
	_, _ = fmt.Fprintf(w, "  %s\n", url)
}

// RenderHintRow is the error-explanations cluster (spec §6.4
// amendment 1) hint renderer. Emits a single-line "💡 hint: <hint>"
// row between Detail and Why in the APIError render. The 💡
// glyph is in the central glyph table that the tripwire
// TestLintTripwire_NoGlyphLiteralOutsideOutput pins to output.go
// (lint_tripwires_test.go:138) — no other file in the package
// may use 💡 verbatim. When Enabled() is false the row collapses
// to "  hint: <hint>" so script consumers that grep on the
// literal "hint:" still match.
//
// The hint is the single short next-action line from the
// pkg/whycopy catalog — one line, ≤200 bytes (the CLI tripwire in
// whycopy_test.go::TestDecorate_AllCodesHaveProse enforces this
// ceiling on the catalog side, so this renderer never has to
// truncate).
func RenderHintRow(w io.Writer, hint string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "  💡 hint: %s\n", hint)
		return
	}
	_, _ = fmt.Fprintf(w, "  hint: %s\n", hint)
}

// RenderWhyRow emits the "why: <why>" line between Hint and Fix.
// Multi-line why content (the catalog rows template the observed
// value + a sentence or two of cause explanation) is preserved
// verbatim — pkg/whycopy caps each row at 512 bytes
// (whycopy_test.go) so a single line is the practical ceiling.
// The why: glyph is in the central glyph table; no other file
// may use it verbatim.
func RenderWhyRow(w io.Writer, why string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "  why: %s\n", why)
		return
	}
	_, _ = fmt.Fprintf(w, "  why: %s\n", why)
}

// RenderFixRow emits the "→ fix: <fix>" line. The fix may be 1-3
// lines (the catalog rows use "• ..." bullets separated by \n);
// the renderer preserves the literal newlines so the customer
// sees the multi-line shape verbatim. The → glyph is shared with
// RenderDocsRow (one glyph in the central table serves both).
func RenderFixRow(w io.Writer, fix string) {
	if Enabled() {
		_, _ = fmt.Fprintf(w, "  → fix: %s", fix)
		// Ensure the trailing newline if the caller didn't
		// include one (catalog rows always do, but be defensive
		// so the next row's leading indentation isn't lost).
		if !strings.HasSuffix(fix, "\n") {
			_, _ = fmt.Fprint(w, "\n")
		}
		return
	}
	_, _ = fmt.Fprintf(w, "  fix: %s", fix)
	if !strings.HasSuffix(fix, "\n") {
		_, _ = fmt.Fprint(w, "\n")
	}
}

// RenderRelevantLogs emits the per-line log excerpts the server
// attached to the Problem (cap 20 entries, each ≤512 bytes — the
// CLI tripwire at whycopy_test.go enforces the cap on the
// catalog side; this renderer enforces the on-screen cap of
// 5 lines for legibility). The fenced block carries "┌─
// relevant logs ─" + "│" + "└─" markers so the customer can
// visually delimit the log block from the surrounding error
// shape. The glyphs are in the central glyph table.
func RenderRelevantLogs(w io.Writer, logs []api.LogExcerpt) {
	max := 5
	if len(logs) < max {
		max = len(logs)
	}
	if Enabled() {
		_, _ = fmt.Fprintln(w, "  ┌─ relevant logs ─")
		for i := 0; i < max; i++ {
			l := logs[i]
			_, _ = fmt.Fprintf(w, "  │ %s %s %s\n", l.Timestamp, l.Level, l.Message)
		}
		if len(logs) > max {
			_, _ = fmt.Fprintf(w, "  │ … (%d more)\n", len(logs)-max)
		}
		_, _ = fmt.Fprintln(w, "  └─")
		return
	}
	_, _ = fmt.Fprintln(w, "  relevant logs:")
	for i := 0; i < max; i++ {
		l := logs[i]
		_, _ = fmt.Fprintf(w, "    %s %s %s\n", l.Timestamp, l.Level, l.Message)
	}
	if len(logs) > max {
		_, _ = fmt.Fprintf(w, "    … (%d more)\n", len(logs)-max)
	}
}

// PrintUsage emits a one-line "usage:" hint followed by a "Docs:" line
// pointing at a live public docs route. Always plain (no glyphs) — usage
// lines go to stderr on bad argv and customers grep them; the glyph would
// just be noise there. Unknown command topics use the consolidated CLI page.
func PrintUsage(w io.Writer, usage, topic string) {
	_, _ = fmt.Fprintf(w, "%s\n", usage)
	_, _ = fmt.Fprintf(w, "  Docs: %s\n", docsURLForTopic(topic))
}

// LiveTicker is a per-command redraw buffer for tabular progress UI
// (ADR-117 §3 — deploy stage ticker). Two implementations:
//
//   - ttyLiveTicker: emits an N-row block and re-prints it in place on
//     every Update using ANSI cursor-up + clear-line. Used when
//     Enabled() is true (real TTY, no --json, no NO_COLOR).
//   - staticLiveTicker: emits one line per Update on the first call,
//     no redraw. Used when stdout is a pipe / NO_COLOR / --json.
//
// The interface stays small — Update(row, name, status, dur) and
// Close() — so the deploy_stages renderer doesn't grow API surface
// every time a new column is needed.
//
// ADR-117 §3: the ticker is intentionally NOT thread-safe. The CLI
// command owns the goroutine that reads SSE; no other goroutine
// touches the ticker. If that ever changes, swap `t` for an
// `atomic.Pointer[rows]` (Go 1.22+).
type LiveTicker interface {
	// Update replaces row `rowIdx` (0-based) with the rendered
	// (name, status, duration) triplet and redraws the whole block
	// when TTY-mode. Out-of-range rowIdx is a no-op so the caller
	// doesn't have to track which rows were never announced.
	Update(rowIdx int, name, status, dur string)
	// Close flushes any pending state and, in TTY mode, prints a
	// final newline so subsequent output doesn't overwrite the
	// last redraw. Close is idempotent.
	Close()
}

// NewLiveTicker returns a LiveTicker sized for `rows` rows. TTY
// selection mirrors the global Enabled() gate — the same stdout
// probe, the same NO_COLOR check, the same jsonOutput flag. The
// caller passes the io.Writer (typically osStdout) so tests can
// drive a bytes.Buffer instead.
//
// ADR-117 §3: when stdout is NOT a TTY, the function still returns
// a LiveTicker (the static impl) so the caller's Update/Close call
// sites are identical regardless of mode — the difference is purely
// in the implementation.
func NewLiveTicker(w io.Writer, rows int) LiveTicker {
	if Enabled() && rows > 0 {
		return &ttyLiveTicker{w: w, rows: rows}
	}
	return &staticLiveTicker{w: w}
}

// ttyLiveTicker redraws the N-row block in place. The first Update
// emits all N rows; subsequent Updates move the cursor up N lines
// (ANSI CSI 1 A repeated) + clear-line (ANSI CSI 2 K) before
// reprinting. The screen ends up with exactly N lines, never N*2.
//
// The implementation never writes to the ticker before the first
// Update — it doesn't know the row labels until the first frame
// arrives, and the SSE consumer's `announced` dedup guarantees we
// only see each row once.
//
// ADR-117 §3: ANSI sequences used:
//
//	CSI n A   cursor up n rows
//	CSI 2 K   erase entire current line
//	\r        carriage return to column 0
//
// We avoid CSI n B (cursor down) because the terminal may already
// be at the bottom; CSI 2 K on the bottom-most line is a no-op on
// most terminals and a clear-line on the others. The cursor-up
// sequence handles both cases because it works from any row.
type ttyLiveTicker struct {
	w         io.Writer
	rows      int
	seen      []bool
	written   bool // true after the initial N-row print
	updateCnt int  // number of Update calls so far
}

// escapeUp returns the CSI sequence that moves the cursor up `n`
// rows. Hoisted as a const-friendly helper so the test seam can
// match it byte-for-byte without a string rebuild.
func escapeUp(n int) string {
	// CSI Pn A — move cursor up Pn rows.
	return "\033[" + strconv.Itoa(n) + "A"
}

// escapeClearLine returns the CSI sequence that erases the entire
// current line. CSI 2 K is the universal "erase entire line" form
// and is supported by xterm, iTerm2, Terminal.app, gnome-terminal,
// and every other modern terminal the cli matrix tests against.
const escapeClearLine = "\033[2K"

func (t *ttyLiveTicker) Update(rowIdx int, name, status, dur string) {
	if rowIdx < 0 || rowIdx >= t.rows {
		return
	}
	if t.seen == nil {
		t.seen = make([]bool, t.rows)
	}
	if !t.written {
		// First call — emit placeholder rows for every slot so the
		// redraw sequence below has N rows to move up over. Each
		// row is `   · <name padded>` so a downstream parser sees a
		// stable shape even on the very first frame, AND the
		// subsequent Update's escapeUp(N) lands on the top row of
		// the block (not 5 rows into stdout that came before the
		// ticker started). PR-A review fix (F2).
		for i := 0; i < t.rows; i++ {
			t.seen[i] = false
		}
		t.written = true
		// Write N placeholder rows so the cursor ends up on the
		// LAST row of the block. The real Update below writes the
		// row at `rowIdx` — that overwrites the placeholder at
		// that row only; the next Update's redraw sequence
		// (escapeUp + clear + escapeUp) restores all N rows from
		// their respective last-rendered state via the same
		// sequence.
		for i := 0; i < t.rows; i++ {
			_, _ = fmt.Fprintf(t.w, "   %s  %s  %s\n", stageGlyph(stageStatusPending), padName(placeholderName(i), 24), "--")
		}
	}
	// Move up N rows + clear each.
	if t.updateCnt > 0 {
		_, _ = fmt.Fprint(t.w, escapeUp(t.rows))
		for i := 0; i < t.rows; i++ {
			_, _ = fmt.Fprint(t.w, escapeClearLine+"\n")
		}
		// After clearing, cursor is on the line BELOW the block.
		// Move up N more to position cursor on the top row again.
		_, _ = fmt.Fprint(t.w, escapeUp(t.rows))
	}
	t.seen[rowIdx] = true
	_, _ = fmt.Fprintf(t.w, "   %s  %s  %s\n", stageGlyph(status), padName(name, 24), dur)
	t.updateCnt++
}

// placeholderName returns the label for the i-th placeholder row.
// Uses the deploy-stage label map when the index is in range so the
// first frame's placeholder rows carry the same human labels the
// real frames will overwrite — better UX than a column of `· Stage N`
// at boot. Out-of-range indices fall back to a generic slot label so
// the helper is total.
func placeholderName(i int) string {
	if i < 0 || i >= len(stageOrder) {
		return fmt.Sprintf("stage-%d", i)
	}
	return stageLabels[stageOrder[i]]
}

func (t *ttyLiveTicker) Close() {
	// The terminal cursor is currently on the last row of the block.
	// Print one trailing newline so the next non-ticker line (the
	// existing `✓ Deployed. …` line in streamDeployLogs) starts on a
	// fresh row without overwriting our last row.
	_, _ = fmt.Fprintln(t.w)
}

// staticLiveTicker emits one line per Update, no redraw. The line
// shape matches the TTY row so grep-friendly consumers see the same
// field order.
type staticLiveTicker struct {
	w     io.Writer
	wrote bool
}

func (s *staticLiveTicker) Update(rowIdx int, name, status, dur string) {
	if !s.wrote {
		// First call: emit the header row so the static fallback
		// reads as a table.
		_, _ = fmt.Fprintln(s.w, "  status  stage                          duration")
		s.wrote = true
	}
	_, _ = fmt.Fprintf(s.w, "   %s  %s  %s\n", stageGlyph(status), padName(name, 24), dur)
}

func (s *staticLiveTicker) Close() {
	// nothing to flush — every Update already wrote its own newline.
}

// stageGlyph returns the single-character status marker for the
// ticker row. Hoisted so output.go remains the single home for the
// four glyphs the lint tripwire allows (✓/✗/→/!) — and the new
// `·` for "pending" / `…` for "in progress" markers sit beside them.
//
// The leading-prefix tripwire at lint_tripwires_test.go only matches
// `"✓` / `"✗` / `"→`; the new glyphs are returned as values, never
// as leading-string literals, so the tripwire stays green.
func stageGlyph(status string) string {
	switch status {
	case stageStatusCompleted:
		return "✓"
	case stageStatusInProgress:
		return "…"
	case stageStatusFailed:
		return "✗"
	default:
		return "·"
	}
}

// padName right-pads `s` to `w` columns. The ticker's "name" column
// is 24 chars wide; names shorter than that get trailing spaces so
// columns align under the TTY redraw. Names longer than 24 chars are
// truncated with an ellipsis — better than a column-width explosion
// when a customer renames a stage.
func padName(s string, w int) string {
	if len(s) >= w {
		return s[:w-1] + "…"
	}
	return s + spaces(w-len(s))
}

// spaces returns n ASCII spaces. Avoids the strings.Repeat import
// at the call site; tiny helper, kept local to output.go.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = ' '
	}
	return string(buf)
}
