package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Help text reaches the PowerShell script as a single-quoted literal. Go's %q
// produced double-quoted strings, which have no \" escape and expand
// $variables, so `--at "2026-10-22 02:00"` broke the whole script.
func TestPowershellCompletionQuotesHelpTextAsLiterals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "plain", want: "'plain'"},
		{in: `release at "2026-10-22 02:00" (local)`, want: `'release at "2026-10-22 02:00" (local)'`},
		{in: "reads $HOME and `n verbatim", want: "'reads $HOME and `n verbatim'"},
		{in: "it's", want: "'it''s'"},
		{in: "it’s", want: "'it’’s'"},
		{in: "‘quoted’", want: "'‘‘quoted’’'"},
	} {
		if got := main.PSLiteral(tc.in); got != tc.want {
			t.Errorf("PSLiteral(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}

	stdout, restore := main.CaptureStdout(t)
	main.CmdCompletionPowershell()
	restore()
	const tooltip = "'ParameterName', "
	for _, line := range strings.Split(stdout.String(), "\n") {
		if i := strings.Index(line, tooltip); i >= 0 && strings.HasPrefix(line[i+len(tooltip):], `"`) {
			t.Fatalf("help text is a double-quoted PowerShell string: %s", line)
		}
	}
}

// PowerShell has no backslash escapes, so a \" anywhere in the script is Go
// quoting leaking through, in a completion value as much as in help text.
func TestPowershellCompletionHasNoGoQuoting(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var buf bytes.Buffer
	main.CaptureStdoutSwap(t, &buf, main.CmdCompletionPowershell)
	for i, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, `\"`) {
			t.Fatalf("line %d holds a Go-escaped quote: %s", i+1, line)
		}
	}
}
