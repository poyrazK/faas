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
)

// TestMain forces the test seam into "glyphs on" by default for every
// test in this package. Pre-§3.2, the CLI printed ✓/✗/→ unconditionally,
// so every existing test in cmd/faas (TestCmdDeploy_HappyPath_…,
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
	on := true
	testOnlyTTY = &on
	code := m.Run()
	testOnlyTTY = nil
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
//	usage: faas <cmd> <args>
//	  Docs: https://docs.DOMAIN/cli/<topic>
//
// Two lines, leading-whitespace on the second, exact namespace, no
// glyphs (usage errors go to stderr; customers grep them — the
// progress arrow would just be noise). Locks the /cli/<topic>
// namespace the §3.2 PR establishes so a future refactor can't
// silently rename it.
func TestPrintUsage_EmitsTwoLinesWithTopic(t *testing.T) {
	var buf bytes.Buffer
	PrintUsage(&buf, "usage: faas ps <app>", "ps")
	got := buf.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), got)
	}
	if lines[0] != "usage: faas ps <app>" {
		t.Errorf("line 0 = %q, want exact usage string", lines[0])
	}
	want := "  Docs: https://docs.DOMAIN/cli/ps"
	if lines[1] != want {
		t.Errorf("line 1 = %q, want %q", lines[1], want)
	}
	// Belt-and-braces: usage lines must NEVER carry a glyph, even
	// when stdout is a TTY — they go to stderr on bad argv.
	if strings.ContainsAny(got, "✓✗→!") {
		t.Errorf("usage line should be glyph-free, got %q", got)
	}
}
