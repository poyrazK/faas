// confirm_test.go — table-driven coverage for requireTyped
// (cmd/gregale/confirm.go).
//
// Pins the three policy decisions that the safety story depends on:
//
//   1. TrimRight of CR/LF only (no TrimSpace). Leading / trailing
//      spaces are treated as a mismatch — the whole point is that
//      a typo (`  delete my account  `) does NOT pass.
//   2. Case-sensitive. The expected string is the exact bytes the
//      caller requires; `Delete My Account` does not match
//      `delete my account`.
//   3. EOF / empty input aborts. A user hitting Ctrl-D (or a CI
//      pipe with no data) is not silently considered "approved" —
//      destruction requires the destructive verb on its own line.

package main

import (
	"strings"
	"testing"
)

func TestRequireTyped_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		input    string
		want     bool
	}{
		{
			name:     "match",
			expected: "delete my account",
			input:    "delete my account\n",
			want:     true,
		},
		{
			name:     "mismatch",
			expected: "delete my account",
			input:    "delete\n",
			want:     false,
		},
		{
			name:     "trailing_whitespace",
			expected: "delete my account",
			input:    "delete my account \n",
			want:     false,
		},
		{
			name:     "leading_whitespace",
			expected: "delete my account",
			input:    " delete my account\n",
			want:     false,
		},
		{
			name:     "case_sensitive",
			expected: "delete my account",
			input:    "Delete My Account\n",
			want:     false,
		},
		{
			name:     "windows_line_endings",
			expected: "delete my account",
			input:    "delete my account\r\n",
			want:     true,
		},
		{
			name:     "eof",
			expected: "delete my account",
			input:    "",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pipeStdin(t, tc.input)

			got := requireTyped(tc.expected)
			if got != tc.want {
				t.Fatalf("requireTyped(%q) with input %q = %v, want %v",
					tc.expected, tc.input, got, tc.want)
			}
		})
	}
}

// TestRequireTyped_PromptShape pins the rendered prompt so we don't
// silently change the user-facing wording. The format string
// `Type %q to confirm: ` (with the expected text quoted via %q) is
// the documented contract — `gh` uses the same shape and screen
// readers / muscle memory depend on it.
func TestRequireTyped_PromptShape(t *testing.T) {
	pipeStdin(t, "delete my account\n")
	rd, restore := captureStderr(t)
	got := requireTyped("delete my account")
	restore()

	if !got {
		t.Fatalf("requireTyped returned false on exact match")
	}
	out := rd.String()
	if !strings.Contains(out, `Type "delete my account" to confirm: `) {
		t.Fatalf("prompt missing or malformed; stderr was %q", out)
	}
}

// production-us hunt #4: piped (non-terminal) stdin names the --quiet bypass
// so a script waiting on the prompt says why in its log.
func TestRequireTyped_NonTerminalNamesTheBypass(t *testing.T) {
	pipeStdin(t, "delete edge rule\n")
	rd, restore := captureStderr(t)
	got := requireTyped("delete edge rule")
	restore()
	if !got || !strings.Contains(rd.String(), "pass --quiet to skip this confirmation") {
		t.Fatalf("requireTyped = %v, stderr %q; want piped input accepted and the --quiet hint shown", got, rd.String())
	}
}

// TestRequireTyped_MismatchPrintsCancel pins that an aborted prompt
// writes a recognisable line on stderr — important for CI logs
// (`Operation cancelled` matches the convention used by
// cmdAccountRestore et al) and for users who scroll back and want
// to know whether they typed wrong or hit Ctrl-C.
func TestRequireTyped_MismatchPrintsCancel(t *testing.T) {
	pipeStdin(t, "wrong\n")
	rd, restore := captureStderr(t)
	got := requireTyped("delete my account")
	restore()

	if got {
		t.Fatalf("requireTyped returned true on mismatch")
	}
	out := rd.String()
	if !strings.Contains(out, "Operation cancelled") {
		t.Fatalf("mismatch path did not print 'Operation cancelled'; stderr was %q", out)
	}
}
