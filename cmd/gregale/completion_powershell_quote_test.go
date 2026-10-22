package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestPSQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{`at "2026-10-22 02:00" (local)`, `'at "2026-10-22 02:00" (local)'`},
		{"it's", "'it''s'"},
		{"don’t", "'don’’t'"},
		{"‘quoted’", "'‘‘quoted’’'"},
		{"$HOME and `n", "'$HOME and `n'"},
	}
	for _, tc := range cases {
		if got := main.PSQuote(tc.in); got != tc.want {
			t.Errorf("PSQuote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// PowerShell has no backslash escapes, so a \" in the script is Go quoting
// leaking through: a description with a double quote (the release --at
// flag's) made the whole script fail to parse.
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
