package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestEnvironmentCreateHelpNamesItsArguments pins Hunt 32 H32-31: the help
// printed "Usage: gregale environment create" with no arguments or flags.
func TestEnvironmentCreateHelpNamesItsArguments(t *testing.T) {
	out, _, restore := main.SwapIO(t)
	defer restore()
	_ = main.Run([]string{"environment", "create", "--help"})
	got := out.String()
	for _, want := range []string{"<project-slug> <environment-slug>", "--from", "--protected", "--share-resources"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help does not mention %q:\n%s", want, got)
		}
	}
}
