package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestBareGroupsListTheirCommands pins Hunt 32 H32-2: `gregale bucket`
// printed `bucket inventory`'s usage and `gregale mcp` that of `mcp server`.
func TestBareGroupsListTheirCommands(t *testing.T) {
	for _, group := range []string{"bucket", "mcp"} {
		_, errOut, restore := main.SwapIO(t)
		code := main.Run([]string{group})
		got := errOut()
		restore()
		if code == 0 || !strings.Contains(got, "Commands:") || strings.Contains(got, "inventory start") || strings.Contains(got, "mcp server [command]") {
			t.Fatalf("gregale %s = %d:\n%s", group, code, got)
		}
	}
}
