package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// App output reached the terminal with its control characters, so a logged
// request field could run escape sequences in whoever tailed the logs (bug
// hunt 2026-10-28 H32).
func TestTerminalSafeEscapesControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttext":                     "plain\ttext",
		"\x1b]52;c;cGF5bG9hZA==\x07 done": `\x1b]52;c;cGF5bG9hZA==\x07 done`,
		"bell\x07 del\x7f c1\u009b":       `bell\x07 del\x7f c1\x9b`,
		"unicode ünïcode ✓":               "unicode ünïcode ✓",
		// A right-to-left override reorders what follows it (ADR-20261110090000).
		"user=\u202egnp.exe": "user=\\u202egnp.exe",
		"iso\u2066x\u2069":   "iso\\u2066x\\u2069",
	} {
		if got := main.TerminalSafe(in); got != want {
			t.Fatalf("TerminalSafe(%q) = %q, want %q", in, got, want)
		}
	}
	line := main.FormatRuntimeLogLine(`{"instance":"abcdef0123","stream":"stdout","line":"GET /\u001b[2J"}`)
	if strings.ContainsRune(line, 0x1b) || !strings.Contains(line, `\x1b[2J`) {
		t.Fatalf("runtime log line = %q, want the escape shown, not sent", line)
	}
}

// A build log entry can hold several lines. Each line break was shown as a
// literal \x0a, running a failed build's explanation together (#6633).
func TestTerminalSafeLinesKeepsLineBreaks(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"using persisted framework profile: node\n", []string{"using persisted framework profile: node"}},
		{"[guest build output]\nnode toolchain HTTPS preflight: timeout\n", []string{"[guest build output]", "node toolchain HTTPS preflight: timeout"}},
		{"crlf one\r\ncrlf two\r\n", []string{"crlf one", "crlf two"}},
		{"blank\n\nkept", []string{"blank", "", "kept"}},
		// Literal backslash text from the build is not a line break.
		{`printf 'a\x0ab\n'`, []string{`printf 'a\x0ab\n'`}},
		// Every other control character is still escaped.
		{"red \x1b[31m\nbell\x07", []string{`red \x1b[31m`, `bell\x07`}},
		{"bare\rreturn", []string{`bare\x0dreturn`}},
	} {
		got := main.TerminalSafeLines(tc.in)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("TerminalSafeLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
