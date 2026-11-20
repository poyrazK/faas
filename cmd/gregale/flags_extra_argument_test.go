package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-19: `flag get --project bh4p newui` said "--project is
// required" although --project was given; the key argument was the problem.
func TestFlagsNamesAnUnexpectedArgument(t *testing.T) {
	_, stderr, restore := main.SwapIO(t)
	code := main.CmdFlags([]string{"get", "--project", "bh4p", "newui"})
	restore()
	got := stderr()
	if code == 0 || !strings.Contains(got, `unexpected argument "newui"`) || strings.Contains(got, "--project is required") {
		t.Fatalf("exit %d stderr %q", code, got)
	}
}
