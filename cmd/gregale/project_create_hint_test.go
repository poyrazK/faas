package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-19: `project create` said "unknown projects subcommand" and
// not how a project comes to exist.
func TestProjectCreateSaysHowProjectsAreCreated(t *testing.T) {
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"project", "create", "bh4p"})
	restore()
	got := stderr()
	if code == 0 || !strings.Contains(got, `unknown project subcommand "create"`) || !strings.Contains(got, "deploy --project") {
		t.Fatalf("exit %d stderr %q", code, got)
	}
}
