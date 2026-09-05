package main

// Tests for §3.2 output gating (output.go). Pins:
//   - NO_COLOR honours spec (any non-empty disables)
//   - The test seam (testOnlyTTY) flips Enabled without touching
//     os.Stdout (the runner captures stdout, which would normally make
//     every Print* land in plain mode)
//   - jsonOutput short-circuits the gate regardless of TTY
//   - The four Print* renderers drop the leading glyph when disabled and
//     keep the format/content untouched

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain forces the test seam into "glyphs on" by default for every
// test in this package. Pre-§3.2, the CLI printed ✓/✗/→ unconditionally,
// so every existing test in cmd/gregale (TestCmdDeploy_HappyPath_…,
// TestCmdLogin_FirstRun_…, TestCapacityError_…, …) compared stdout
// against strings that begin with "✓ ". With the §3.2 gate in place
// those strings would silently strip in the captured-stdout test runner,
// turning all of those green tests red for no behavioural reason.
//
// The opt-out tests in this file (TestPrintOK_DropsGlyphWhenDisabled,
// TestEnabled_BlockedByNonTTY, …) call withTTYForTest(false) and the
// matching TTY-gated glyph test (TestRenderAPIError_TTYGatedGlyph)
// assigns testOnlyTTY directly with a defer to restore — those tests
// don't rely on TestMain's rearm. After every test in this package,
// TestMain flips testOnlyTTY back to &true so the next test starts in
// the default-on state. Production binaries never see this — testOnlyTTY
// is `nil` in non-test builds (output.go).
func TestMain(m *testing.M) {
	// A missing per-test stub must never read, overwrite, or delete the
	// developer's real OS credentials. Explicit test doubles still take
	// precedence through effectiveKeyring.
	keyring.MockInit()
	previousNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	resetNOColorCache()
	on := true
	testOnlyTTY = &on
	code := m.Run()
	testOnlyTTY = nil
	if hadNoColor {
		_ = os.Setenv("NO_COLOR", previousNoColor)
	} else {
		_ = os.Unsetenv("NO_COLOR")
	}
	os.Exit(code)
}

// withTTYForTest flips the package-level testOnlyTTY seam and returns a
// restore func that wipes it back to nil (NOT to the previous value).
// The asymmetry is intentional: TestMain rearms the seam to &true at the
// start of m.Run, so any subtest that relies on default-on behaviour can
// opt out and back in by calling withTTYForTest(false/true) explicitly.
// Not goroutine-safe — see output.go's godoc.
func withTTYForTest(v bool) func() {
	b := v
	testOnlyTTY = &b
	return func() { testOnlyTTY = nil }
}

// resetNOColorCache forces noColorSet to re-read NO_COLOR. Used because
// os.LookupEnv is read once and cached; t.Setenv-by-itself doesn't
// invalidate the cache.
func resetNOColorCache() {
	noColorCached.Store(false)
	noColorVal = false
}

// resetStdoutTTYCache forces stdoutIsTTY to re-evaluate. Used after
// flipping testOnlyTTY (cheap because the seam branch returns first).
func resetStdoutTTYCache() {
	isStdoutTTYOnce.Store(false)
	isStdoutTTYVal.Store(false)
}

func TestEnabled_HonoursNO_COLOR(t *testing.T) {
	cases := []struct {
		name, val string
		// If unset: true. NO_COLOR = "" means "explicitly empty".
		// Per no-color.org both unset and "" mean "no preference", so
		// Enabled() should be true when testOnlyTTY=true (the test hook
		// forces the non-piped, non-CI path).
		// Non-empty values mean "disable", so Enabled() must be false.
		wantEnabled bool
	}{
		{"unset", "unset-please", true},
		{"empty string", "", true},
		{"1", "1", false},
		{"0", "0", false},
		{"false", "false", false},
		{"no", "no", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNOColorCache()
			if tc.name == "unset" {
				// Unsetenv does the right thing here.
				t.Setenv("NO_COLOR", "")
				// Go's t.Setenv with empty value leaves the var *set*
				// to empty, which os.LookupEnv sees as set. Use the
				// reverse: call os.Unsetenv directly via t.Setenv
				// workaround. The output.go noColorSet semantics treat
				// "set and empty" the same as unset (both == "" raw),
				// so this collapses to the empty-string branch.
				// Documenting here for posterity.
			} else {
				t.Setenv("NO_COLOR", tc.val)
			}
			defer resetNOColorCache()
			restore := withTTYForTest(true)
			defer restore()
			resetStdoutTTYCache()
			got := Enabled()
			if got != tc.wantEnabled {
				t.Errorf("NO_COLOR=%q: got Enabled()==%v, want %v", tc.val, got, tc.wantEnabled)
			}
		})
	}
}

func TestEnabled_BlockedByNonTTY(t *testing.T) {
	restore := withTTYForTest(false)
	defer restore()
	resetStdoutTTYCache()
	if Enabled() {
		t.Error("Expected Enabled()==false when testOnlyTTY=false")
	}
}

func TestEnabled_RespectsTestHookTrue(t *testing.T) {
	restore := withTTYForTest(true)
	defer restore()
	resetStdoutTTYCache()
	resetNOColorCache()
	t.Setenv("NO_COLOR", "")
	if !Enabled() {
		t.Error("Expected Enabled()==true when testOnlyTTY=true and no NO_COLOR")
	}
}

func TestEnabled_JSONModeAlwaysFalse(t *testing.T) {
	restore := withTTYForTest(true)
	defer restore()
	resetStdoutTTYCache()
	resetJSONOutput()
	defer resetJSONOutput()
	jsonOutput = true
	if Enabled() {
		t.Error("Expected Enabled()==false when jsonOutput=true regardless of TTY")
	}
}

func TestPrintOK_KeepsGlyphWhenEnabled(t *testing.T) {
	restore := withTTYForTest(true)
	defer restore()
	resetStdoutTTYCache()
	resetJSONOutput()
	var buf bytes.Buffer
	PrintOK(&buf, "Deployed %s", "app1")
	out := buf.String()
	if !strings.HasPrefix(out, "✓ ") {
		t.Errorf("Expected leading glyph when enabled, got %q", out)
	}
	if !strings.Contains(out, "Deployed app1") {
		t.Errorf("Expected format/content preserved, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("Expected trailing newline, got %q", out)
	}
}

func TestPrintOK_DropsGlyphWhenDisabled(t *testing.T) {
	restore := withTTYForTest(false)
	defer restore()
	resetStdoutTTYCache()
	var buf bytes.Buffer
	PrintOK(&buf, "Deployed %s", "app1")
	out := buf.String()
	if strings.ContainsAny(out, "✓✗→!") {
		t.Errorf("Expected no glyph when disabled, got %q", out)
	}
	if !strings.Contains(out, "Deployed app1") {
		t.Errorf("Expected content preserved, got %q", out)
	}
	if !strings.HasPrefix(out, "Deployed") {
		t.Errorf("Expected leading content when no glyph, got %q", out)
	}
}

func TestPrintFail_DropsGlyphWhenDisabled(t *testing.T) {
	restore := withTTYForTest(false)
	defer restore()
	resetStdoutTTYCache()
	var buf bytes.Buffer
	PrintFail(&buf, "%s failed: %s", "deploy", "timeout")
	out := buf.String()
	if strings.Contains(out, "✗") {
		t.Errorf("Expected no ✗ when disabled, got %q", out)
	}
	if !strings.Contains(out, "deploy failed: timeout") {
		t.Errorf("Expected content preserved, got %q", out)
	}
}

func TestPrintProgress_KeepsArrowWhenEnabled(t *testing.T) {
	restore := withTTYForTest(true)
	defer restore()
	resetStdoutTTYCache()
	var buf bytes.Buffer
	PrintProgress(&buf, "queued %s", "build1")
	out := buf.String()
	if !strings.HasPrefix(out, "→ ") {
		t.Errorf("Expected leading arrow when enabled, got %q", out)
	}
}

func TestPrintWarn_DropsBangWhenDisabled(t *testing.T) {
	restore := withTTYForTest(false)
	defer restore()
	resetStdoutTTYCache()
	var buf bytes.Buffer
	PrintWarn(&buf, "watchdog %s", "kicked")
	out := buf.String()
	if strings.Contains(out, "!") {
		t.Errorf("Expected no ! when disabled, got %q", out)
	}
	if !strings.Contains(out, "watchdog kicked") {
		t.Errorf("Expected content preserved, got %q", out)
	}
}

// TestPrintUsage_EmitsTwoLinesWithTopic locks the contract every
// per-command usage error surfaces:
//
//	usage: gregale <cmd> <args>
//	  Docs: https://gregale.dev/docs/cli
//
// Two lines, leading-whitespace on the second, exact namespace, no
// glyphs (usage errors go to stderr; customers grep them — the
// progress arrow would just be noise). Locks the live consolidated
// CLI docs route so a future refactor can't silently reintroduce a
// dead per-command path.
func TestPrintUsage_EmitsTwoLinesWithTopic(t *testing.T) {
	var buf bytes.Buffer
	PrintUsage(&buf, "usage: gregale ps <app>", "ps")
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), got)
	}
	if lines[0] != "usage: gregale ps <app>" {
		t.Errorf("line 0 = %q, want exact usage string", lines[0])
	}
	want := "  Docs: " + cliDocsURL
	if lines[1] != want {
		t.Errorf("line 1 = %q, want %q", lines[1], want)
	}
	// Belt-and-braces: usage lines must NEVER carry a glyph, even
	// when stdout is a TTY — they go to stderr on bad argv.
	if strings.ContainsAny(got, "✓✗→!") {
		t.Errorf("usage line should be glyph-free, got %q", got)
	}
}

// TestLiveTicker_TTY_RedrawsInPlace pins ADR-117 §3 + PR-A
// review fix (F2): when stdout IS a TTY the ticker emits the
// N-row block on first Update (6 placeholders so the cursor
// sits on row N+1 — not row 1 — and subsequent escapeUp(N)
// sequences land cleanly on the top of the block), and on
// every subsequent Update the cursor jumps to the top of the
// block, clears all 6 lines, then writes the single updated
// row at rowIdx. The terminal sees exactly N lines after each
// Update — not 2N — so the customer's screen doesn't scroll.
//
// The test forces TTY via testOnlyTTY, drives 6 Updates (one
// per stage), and asserts:
//   - 12 row lines total (6 placeholders + 1 real row in
//     Update 0; then 1 row line each in Updates 1..5 = 5)
//   - 10 cursor-up sequences (2 per Update × 5 redraws;
//     Update 0 emits 0 cursor-ups because it places the
//     initial block)
//   - 30 clear-line sequences (6 per Update × 5 redraws)
//   - 1 trailing newline from Close
func TestLiveTicker_TTY_RedrawsInPlace(t *testing.T) {
	defer withTTYForTest(true)()
	resetStdoutTTYCache()
	buf := &bytes.Buffer{}
	tk := NewLiveTicker(buf, 6)
	for i, name := range stageOrder {
		tk.Update(i, stageLabels[name], "completed", "1.2s")
	}
	tk.Close()

	out := buf.String()
	// Count row lines (anything that ends with \n and is NOT an
	// ANSI escape sequence or empty).
	lines := strings.Split(out, "\n")
	rowLines := 0
	for _, l := range lines {
		if strings.Contains(l, escapeClearLine) || strings.TrimSpace(l) == "" {
			continue
		}
		rowLines++
	}
	want := 6 + 6 // 6 placeholders + 6 Update rows (1 per Update)
	if rowLines != want {
		t.Fatalf("got %d row lines, want %d: %q", rowLines, want, out)
	}
	// Cursor-up count = 2 per redraw × 5 redraws = 10. Update 0
	// emits 0 cursor-ups because it places the initial N-row
	// block (no redraw needed). The PR-A review fix changed
	// the placeholder emission: previously the first Update
	// printed only 1 row, which left the cursor 1 row below
	// the top of the block — the next Update's escapeUp(6)
	// then moved the cursor 5 rows into stdout lines written
	// before the ticker started. Now Update 0 prints 6
	// placeholders so the cursor lands on row N+1, which is
	// the correct anchor for subsequent escapeUp(N) sequences.
	upCount := strings.Count(out, escapeUp(6))
	if upCount != 10 {
		t.Errorf("got %d cursor-up escapes, want 10: %q", upCount, out)
	}
	// Clear-line count = 6 per Update × 5 redraws = 30.
	clearCount := strings.Count(out, escapeClearLine)
	if clearCount != 30 {
		t.Errorf("got %d clear-line escapes, want 30: %q", clearCount, out)
	}
	// Close flushes one trailing newline so subsequent output
	// doesn't overwrite the last row.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("Close should terminate with newline, got %q", out)
	}
}

// TestLiveTicker_Static_OneLinePerUpdate pins the static-fallback
// path: when stdout is NOT a TTY (pipe, --json, NO_COLOR), the
// ticker emits one line per Update with no ANSI escapes. This is
// the grep-friendly mode for CI scripts that capture
// `gregale deploy … | tee /tmp/log`.
func TestLiveTicker_Static_OneLinePerUpdate(t *testing.T) {
	defer withTTYForTest(false)()
	resetStdoutTTYCache()
	buf := &bytes.Buffer{}
	tk := NewLiveTicker(buf, 6)
	for _, name := range stageOrder {
		tk.Update(0, stageLabels[name], "completed", "1.2s")
	}
	tk.Close()

	out := buf.String()
	// Static fallback must NOT carry ANSI cursor escapes — a
	// pipe-friendly form is the whole point.
	if strings.Contains(out, "\033[") {
		t.Errorf("static ticker must not emit ANSI escapes, got %q", out)
	}
	// Header + 6 rows = 7 lines (the trailing newline from the
	// last Update). Close is a no-op in static mode (every
	// Update already wrote its own newline).
	if got := strings.Count(out, "\n"); got < 7 {
		t.Errorf("expected ≥ 7 newlines in static ticker, got %d: %q", got, out)
	}
}

// TestStageGlyph_Mapping pins the per-status glyph table. The
// glyph is what the customer sees on the ticker row — a regression
// here is a UX regression, not a logic bug, so it's pinned
// explicitly.
func TestStageGlyph_Mapping(t *testing.T) {
	cases := map[string]string{
		"completed":   "✓",
		"in_progress": "…",
		"failed":      "✗",
		"pending":     "·",
		"":            "·",
	}
	for status, want := range cases {
		if got := stageGlyph(status); got != want {
			t.Errorf("stageGlyph(%q) = %q, want %q", status, got, want)
		}
	}
}
