package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A flag whose description already says "(repeatable)" is not labeled twice.
func TestHelpSaysRepeatableOnce(t *testing.T) {
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	var out bytes.Buffer
	(*main.OsStdout), (*main.OsStderr) = &out, &out
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr })
	main.Run([]string{"realtime", "create", "--help"})
	help := out.String()
	if !strings.Contains(help, "--auth-audience") {
		t.Fatalf("help does not list --auth-audience:\n%s", help)
	}
	if strings.Contains(help, "(repeatable) (repeatable)") {
		t.Fatalf("help repeats (repeatable):\n%s", help)
	}
}
