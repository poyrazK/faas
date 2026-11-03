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
