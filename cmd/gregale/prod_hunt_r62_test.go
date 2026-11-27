package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// `job create --command "echo a; echo b"` parses to one argument that the
// guest execs as a program name, so every task fails (prod hunt r62). The CLI
// warns and names the shell form.
func TestJobCommandWarnsOnAOneArgumentShellLine(t *testing.T) {
	for _, tc := range []struct {
		value string
		warn  bool
	}{
		{"echo task=$I; sleep 2; echo done", true},
		{"python main.py", true},
		{"/app/run", false},
		{"/bin/sh,-c,echo a; echo b", false},
		{`["/bin/sh","-c","echo a; echo b"]`, false},
	} {
		_, stderr, restore := main.SwapIO(t)
		main.WarnShellLineCommand(main.ParseJobCommand(tc.value))
		restore()
		got := stderr()
		if tc.warn != strings.Contains(got, "/bin/sh,-c,<line>") {
			t.Errorf("--command %q: warn=%v, stderr %q", tc.value, tc.warn, got)
		}
	}
}
