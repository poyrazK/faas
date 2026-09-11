// man_test.go — coverage pass for cmd/gregalectl/man.go
// (Cluster 5c of the gregalectl coverage depth-pass, follow-on to
// PR #1044).
//
// Pins the Tier A8 / ADR-083 roff emission contract:
//   - cmdMan dispatcher (no arg, valid arg, invalid arg with
//     suggestion, too-many-args)
//   - lookupCliCommand (hit + miss)
//   - renderManTop (header + 7 sections)
//   - renderManCommand (per-section shape with/without subcommands
//     and flags)
//   - manHeader / manSection / manFooter (atomic roff primitives)
//   - escapeRoff (backslash + leading-period escape)
//   - suggestCommand / suggestSubcommand (Levenshtein-≤2 +
//     tie-suppress policy)
//   - levenshtein / min3 (DP table correctness)
//
// No source changes; mirrors the whitebox pattern (package main)
// used by commands_pki_test.go and commands_compute_nodes_test.go.
package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// resetManGlobals swaps the osStdout package var for the duration
// of the test so cmdMan's renderManTop/Render output is captured.
func resetManGlobals(t *testing.T) {
	t.Helper()
	prev := osStdout
	t.Cleanup(func() { osStdout = prev })
}

// TestCmdMan_NoArgs pins the no-arg branch (renderManTop, exit 0).
func TestCmdMan_NoArgs(t *testing.T) {
	resetManGlobals(t)
	var buf bytes.Buffer
	osStdout = &buf
	if code := cmdMan(nil); code != 0 {
		t.Errorf("cmdMan(nil) = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{".TH GREGALE 1", ".SH NAME", ".SH SYNOPSIS", ".SH DESCRIPTION", ".SH COMMANDS", ".SH GLOBAL FLAGS", ".SH EXAMPLES", ".SH SEE ALSO"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderManTop missing section %q", want)
		}
	}
}

// TestCmdMan_ValidCommand pins the per-command branch. Picks the
// "manifest" command (always present in cliCommands) and asserts
// the per-command header includes "GREGALE-MANIFEST 1".
func TestCmdMan_ValidCommand(t *testing.T) {
	resetManGlobals(t)
	var buf bytes.Buffer
	osStdout = &buf
	if code := cmdMan([]string{"manifest"}); code != 0 {
		t.Errorf("cmdMan(manifest) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "GREGALE-MANIFEST 1") {
		t.Errorf("renderManCommand missing title:\n%s", buf.String())
	}
}

// TestCmdMan_UnknownCommand pins the unknown-command path. With a
// single-character typo distance ≤2 to a real command, the
// dispatcher should print "unknown command" + "Did you mean" hint.
func TestCmdMan_UnknownCommand(t *testing.T) {
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prevStderr })
	// "manifst" is one transposition from "manifest" → Levenshtein 2
	code := cmdMan([]string{"manifst"})
	_ = w.Close()
	var buf bytes.Buffer
	tmp := make([]byte, 1024)
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
		t.Errorf("cmdMan(manifst) = %d, want 1 (unknown)", code)
	}
	got := buf.String()
	if !strings.Contains(got, "unknown command") {
		t.Errorf("unknown command stderr missing header: %q", got)
	}
	if !strings.Contains(got, "Did you mean") {
		t.Errorf("unknown command stderr missing Did-you-mean hint: %q", got)
	}
}

// TestCmdMan_TooManyArgs pins the usage-error path (exit 1,
// PrintUsage → "Docs:" line).
func TestCmdMan_TooManyArgs(t *testing.T) {
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prevStderr })
	code := cmdMan([]string{"manifest", "validate"})
	_ = w.Close()
	var buf bytes.Buffer
	tmp := make([]byte, 1024)
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
		t.Errorf("cmdMan(2 args) = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "Docs:") {
		t.Errorf("usage-error stderr missing Docs hint: %q", buf.String())
	}
}

// TestLookupCliCommand pins the linear scan: hit + miss.
func TestLookupCliCommand(t *testing.T) {
	if _, ok := lookupCliCommand("manifest"); !ok {
		t.Errorf("lookupCliCommand(manifest) = not found, want ok")
	}
	if _, ok := lookupCliCommand("does-not-exist"); ok {
		t.Errorf("lookupCliCommand(does-not-exist) = found, want not ok")
	}
}

// TestManHeader pins the .TH emission: title, section, version
// (gregalectlVersion), source brand.
func TestManHeader(t *testing.T) {
	var buf bytes.Buffer
	manHeader(&buf, "TEST(1)", "test sub", "gregale")
	got := buf.String()
	if !strings.Contains(got, ".TH TEST 1") {
		t.Errorf("manHeader missing title section: %q", got)
	}
	if !strings.Contains(got, "gregale") {
		t.Errorf("manHeader missing source brand: %q", got)
	}
	if !strings.Contains(got, gregalectlVersion) {
		t.Errorf("manHeader missing version %q: %q", gregalectlVersion, got)
	}
}

// TestManSection pins the .SH wrapper. The body callback receives w
// and emits the section content.
func TestManSection(t *testing.T) {
	var buf bytes.Buffer
	manSection(&buf, "name", func(w io.Writer) {
		_, _ = w.Write([]byte("body line\n"))
	})
	got := buf.String()
	if !strings.Contains(got, ".SH NAME\n") {
		t.Errorf("manSection missing .SH NAME: %q", got)
	}
	if !strings.Contains(got, "body line") {
		t.Errorf("manSection missing body: %q", got)
	}
}

// TestEscapeRoff pins the two escape rules:
//   - backslash → double-backslash
//   - leading-period → "\&" prefix (roff "transparent" marker)
func TestEscapeRoff(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain text", "plain text"},
		{`back\slash`, `back\\slash`},
		{".leading dot", `\&.leading dot`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := escapeRoff(tc.in); got != tc.want {
				t.Errorf("escapeRoff(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSuggestCommand pins the Levenshtein-≤2 + tie-suppress policy.
func TestSuggestCommand(t *testing.T) {
	cases := []struct {
		query string
		want  string
		ok    bool
	}{
		{"manifest", "manifest", true}, // exact match → distance 0
		{"manifst", "manifest", true},  // 1-edit deletion (drop 'e')
		{"xyz", "", false},             // over threshold (all commands > 2)
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got, ok := suggestCommand(tc.query)
			if ok != tc.ok {
				t.Errorf("suggestCommand(%q) ok = %v, want %v (got %q)", tc.query, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("suggestCommand(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestLevenshtein pins the DP table correctness on a small set.
func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"manifest", "manifst", 1}, // delete 'e'
		{"abc", "xbc", 1},          // substitute a→x
	}
	for _, tc := range cases {
		t.Run(tc.a+"_"+tc.b, func(t *testing.T) {
			if got := levenshtein(tc.a, tc.b); got != tc.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestMin3 pins the ternary helper.
func TestMin3(t *testing.T) {
	cases := []struct {
		a, b, c, want int
	}{
		{1, 2, 3, 1},
		{3, 2, 1, 1},
		{2, 3, 1, 1},
		{5, 5, 5, 5},
		{-1, 0, 1, -1},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			if got := min3(tc.a, tc.b, tc.c); got != tc.want {
				t.Errorf("min3(%d, %d, %d) = %d, want %d", tc.a, tc.b, tc.c, got, tc.want)
			}
		})
	}
}
