// output_test.go — coverage pass for cmd/gregalectl/output.go
// (Cluster 5b of the gregalectl coverage depth-pass, follow-on to
// PR #1044).
//
// Pins the UX §3.2 contract for the operator-side CLI:
//   - PrintOK/PrintFail/PrintProgress/PrintWarn shape
//   - Enabled() gate (jsonOutput → off, NO_COLOR= → off, TTY → on)
//   - writeStatus: leading glyph + space + content + newline, glyph
//     stripped when Enabled() returns false
//   - PrintUsage: "usage:" line + "Docs: <URL>" line
//   - printErr: "title: err" shape, exit 1
//   - GlyphOK/Fail/Progress/EmDash constants
//
// No source changes; mirrors the whitebox pattern (package main)
// used by commands_pki_test.go and commands_compute_nodes_test.go.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// resetOutputGlobals resets the package-level globals that gate
// output rendering. Mirrors the precedent at
// commands_compute_nodes_test.go:51 (resetMemStore) — called inside
// every t.Run so subtests don't bleed.
func resetOutputGlobals(t *testing.T) {
	t.Helper()
	prevJSON := jsonOutput
	prevTTY := testOnlyTTY
	t.Cleanup(func() {
		jsonOutput = prevJSON
		testOnlyTTY = prevTTY
	})
	jsonOutput = false
	noColorCached.Store(false)
	noColorVal = false
	testOnlyTTY = nil
}

// withTTY installs a testOnlyTTY override for the duration of the
// test and resets the noColor cache (so NO_COLOR is not honoured).
func withTTY(t *testing.T, v bool) {
	t.Helper()
	resetOutputGlobals(t)
	testOnlyTTY = &v
}

// TestPrintOK_GlyphWhenEnabled pins the ✓ glyph path. With
// testOnlyTTY=true and no NO_COLOR, PrintOK emits "✓ done".
func TestPrintOK_GlyphWhenEnabled(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		want string
	}{
		{"tty_on", true, "✓ done"},
		{"tty_off", false, "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)
			var buf bytes.Buffer
			PrintOK(&buf, "done")
			if got := buf.String(); got != tc.want+"\n" {
				t.Errorf("PrintOK = %q, want %q", got, tc.want+"\n")
			}
		})
	}
}

// TestPrintFail_GlyphWhenEnabled pins the ✗ glyph path.
func TestPrintFail_GlyphWhenEnabled(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		want string
	}{
		{"tty_on", true, "✗ bad"},
		{"tty_off", false, "bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)
			var buf bytes.Buffer
			PrintFail(&buf, "bad")
			if got := buf.String(); got != tc.want+"\n" {
				t.Errorf("PrintFail = %q, want %q", got, tc.want+"\n")
			}
		})
	}
}

// TestPrintProgress_GlyphWhenEnabled pins the → glyph path.
func TestPrintProgress_GlyphWhenEnabled(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		want string
	}{
		{"tty_on", true, "→ working"},
		{"tty_off", false, "working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)
			var buf bytes.Buffer
			PrintProgress(&buf, "working")
			if got := buf.String(); got != tc.want+"\n" {
				t.Errorf("PrintProgress = %q, want %q", got, tc.want+"\n")
			}
		})
	}
}

// TestPrintWarn_GlyphWhenEnabled pins the ! glyph path. No leading
// space in the source ("!" is intentionally shorter than the others).
func TestPrintWarn_GlyphWhenEnabled(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		want string
	}{
		{"tty_on", true, "! heads up"},
		{"tty_off", false, "heads up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)
			var buf bytes.Buffer
			PrintWarn(&buf, "heads up")
			if got := buf.String(); got != tc.want+"\n" {
				t.Errorf("PrintWarn = %q, want %q", got, tc.want+"\n")
			}
		})
	}
}

// TestEnabled_JSONOutputOff pins that jsonOutput=true short-circuits
// Enabled() to false regardless of TTY state.
func TestEnabled_JSONOutputOff(t *testing.T) {
	resetOutputGlobals(t)
	jsonOutput = true
	tt := true
	testOnlyTTY = &tt
	if Enabled() {
		t.Errorf("Enabled() = true with jsonOutput=true, want false")
	}
}

// TestEnabled_NOColorOff pins that NO_COLOR=anything forces
// Enabled() to false.
func TestEnabled_NOColorOff(t *testing.T) {
	resetOutputGlobals(t)
	t.Setenv("NO_COLOR", "1")
	noColorCached.Store(false)
	noColorVal = false
	tt := true
	testOnlyTTY = &tt
	if Enabled() {
		t.Errorf("Enabled() = true with NO_COLOR set, want false")
	}
}

// TestPrintUsage_Shape pins the "usage:" line + "Docs: <URL>" line
// shape. Topic appended to docsURLBase.
func TestPrintUsage_Shape(t *testing.T) {
	var buf bytes.Buffer
	PrintUsage(&buf, "usage: gregalectl foo", "foo")
	got := buf.String()
	if !strings.Contains(got, "usage: gregalectl foo\n") {
		t.Errorf("PrintUsage missing usage line: %q", got)
	}
	if !strings.Contains(got, "Docs: https://gregale.dev/docs/cli/foo") {
		t.Errorf("PrintUsage missing docs line: %q", got)
	}
}

// TestPrintErr_NilErr pins the "title only" branch. Returns 1.
func TestPrintErr_NilErr(t *testing.T) {
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prevStderr })
	code := printErr("plain title", nil)
	_ = w.Close()
	var buf bytes.Buffer
	tmp := make([]byte, 256)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			break
		}
	}
	_ = r.Close()
	if code != 1 {
		t.Errorf("printErr(nil) = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "plain title") {
		t.Errorf("printErr stderr missing title: %q", buf.String())
	}
}

// TestGlyphConstants pins the allow-listed glyph literals.
func TestGlyphConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"OK", GlyphOK, "✓"},
		{"Fail", GlyphFail, "✗"},
		{"Progress", GlyphProgress, "→"},
		{"EmDash", GlyphEmDash, "—"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("Glyph%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}
